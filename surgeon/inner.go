// Inner types, classes, and surgical primitives for operating on repository DAGs.
// Also, the code for deserializing a dumnp fuile to a DAG and
// serializing a DAG to a dump file.  Nothing in here is dependent on
// the DSL surface syntax or Kommandant.  It does assume logit(),
// announce(), croak(), respond(), throw() and catch() do sane things.
// Many functions are passed a Baton reference so they can ship
// progress reports.

//
// Copyright by Eric S. Raymond
// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"container/heap"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	shlex "github.com/anmitsu/go-shlex"
	orderedset "github.com/emirpasic/gods/sets/linkedhashset"
	difflib "github.com/ianbruene/go-difflib/difflib"
	shutil "github.com/termie/go-shutil"
	fqme "gitlab.com/esr/fqme"
)

// Tuning constants and types

// Maximum number of 64-bit things (pointers) to allocate at once.
// Used in some code for efficient exponential chunk grabbing.
const maxAlloc = 100000

// Short types for these saves space in very large arrays of repository structures.
// But they're mainly here to avoid strings, which are expensive (16 bytes) in Go.
type markidx uint32 // Mark indicies
type blobidx uint32 // Blob indices. Should not be narrower than mark indices.
type revidx uint32  // Revision indices
// Large repositories can have more than 65536 nodes within a
// revision, especially after expansion of SVN directory copies, so it
// is not safe for this to be uint16.
type nodeidx uint32 // Node indices within revisions.

// Use these for distinguished extreme values of indices wuth int type
const (
	maxInt  = 1<<(bits.UintSize-1) - 1 // 1<<31 - 1 or 1<<63 - 1
	minInt  = -maxInt - 1              // -1 << 31 or -1 << 63
	maxUint = 1<<bits.UintSize - 1     // 1<<32 - 1 or 1<<64 - 1
)

// Go's panic/defer/recover feature is a weak primitive for catchable
// exceptions, but it's all we have. So we write a throw/catch pair;
// throw() must pass its exception payload to panic(), catch() can only be
// called in a defer hook either at the current level or further up the
// call stack and must take recover() as its second argument.
//
// Here are the defined error classes:
//
// parse = malformed fast-import stream or Subversion dump.  Recover
// by aborting the parse and backing out whatever operation relied on it.
//
// command = general command failure, no specific cleanup required, abort
// to interpreter read-eval loop.
//
// extractor = unexpected VCS command behavior inside an extractor class.
// Abort the attempt to build a repo.
//
// msgbox = malformed mailbox-style input.  Abort merging those changes.
//
// Unlabeled panics are presumed to be unrecoverable and intended to be
// full aborts indicating a serious internal error.  These will call a defer
// hook that nukes the on-disk storage for repositories.

type exception struct {
	class   string
	message string
}

func (e exception) Error() string {
	return e.message
}

func throw(class string, msg string, args ...interface{}) *exception {
	// We could call panic() in here but we leave it at the callsite
	// to clue the compiler in that no return after is required.
	e := new(exception)
	e.class = class
	e.message = fmt.Sprintf(msg, args...)
	return e
}

func catch(accept string, x interface{}) *exception {
	// Because recover() returns interface{}.
	// Return us to the world of type safety.
	if x == nil {
		return nil
	}
	if err, ok := x.(*exception); ok {
		if err.class == accept {
			return err
		}
		fmt.Fprintf(os.Stderr, "Somebody threw a %s exception while we were awaiting a %s exception!\n", err.class, accept)
	}
	panic(x)
}

// Change these in the unlikely the event this is ported to Windows
const userReadWriteMode = 0644       // rw-r--r--
const userReadWriteSearchMode = 0775 // rwxrwxr-x

func exists(pathname string) bool {
	_, err := os.Stat(pathname)
	return !os.IsNotExist(err)
}

func isdir(pathname string) bool {
	st, err := os.Stat(pathname)
	return err == nil && st.Mode().IsDir()
}

func isfile(pathname string) bool {
	st, err := os.Stat(pathname)
	return err == nil && st.Mode().IsRegular()
}

func islink(pathname string) bool {
	st, err := os.Stat(pathname)
	return err == nil && (st.Mode()&os.ModeSymlink) != 0
}

func relpath(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	wd, err = filepath.Rel(wd, dir)
	if err != nil {
		panic(err)
	}
	return wd
}

func abspath(dir string) string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	wd, err = filepath.Abs(dir)
	if err != nil {
		panic(err)
	}
	return wd
}

func getsize(pathname string) int64 {
	st, err := os.Stat(pathname)
	if err != nil {
		return -1
	}
	return st.Size()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// filecopy does what it says, returning bytes copied and an error indication
func filecopy(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()
	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

// stringCopy forces crearion of a copy of the input strimg.  This is
// useful because the Go runtime tries not to do more allcations tn
// necessary, making string-valued references instead. Thus,
// sectioning a small string out of a very large one may cause
// the large string to be held in memory even thouggh the rest of the
// contnt is no longer referenced.
func stringCopy(a string) string {
	return (a + " ")[:len(a)]
}

// getAttr emulates Python hasattr/getattr using the Go reflection system
// Current version can only return string-valued fields.
func getAttr(obj interface{}, fld string) (string, bool) {
	objValue := reflect.Indirect(reflect.ValueOf(obj))
	objType := objValue.Type()
	_, ok := objType.FieldByName(fld)
	if !ok {
		return "", false
	}
	return objValue.FieldByName(fld).String(), true
}

func setAttr(obj interface{}, name string, value string) error {
	rv := reflect.ValueOf(obj).Elem()

	structFieldValue := rv.FieldByName(name)

	if !structFieldValue.IsValid() {
		return fmt.Errorf("No such field: %s in obj", name)
	}
	// If obj field value is not settable an error is thrown
	if !structFieldValue.CanSet() {
		return fmt.Errorf("Cannot set %s field value", name)
	}

	f2v := rv.FieldByName(name)
	f2v.SetString(value)
	return nil
}

// stringEscape interprets backslash escapes in a token, such as is returned by
// the shlex package.  If the argument token was wrapped by Go string quotes
// they are stripped off.
func stringEscape(s string) (string, error) {
	if s == "" || s[0] != '"' {
		s = `"` + s + `"`
	}
	return strconv.Unquote(s)
}

// splitRuneFirst splits the string on the rune and returns the first
// substring, but without allocating a slice of all the substrings,
// and without iterating over the string twice
func splitRuneFirst(s string, sep rune) (first string, rest string) {
	idx := strings.IndexRune(s, sep)
	if idx == -1 {
		return s, ""
	}
	return s[:idx], s[idx:]
}

// A copy of the orderedStringSet code with the names changed to protect the innocent.
// Lack of generics is annoying.
type orderedIntSet []int

func newOrderedIntSet(elements ...int) orderedIntSet {
	set := make([]int, 0, len(elements))
	return append(set, elements...)
}

func (s orderedIntSet) Contains(item int) bool {
	for _, el := range s {
		if item == el {
			return true
		}
	}
	return false
}

func (s *orderedIntSet) Remove(item int) bool {
	for i, el := range *s {
		if item == el {
			copy((*s)[i:], (*s)[i+1:])
			*s = (*s)[:len(*s)-1]
			return true
		}
	}
	return false
}

func (s *orderedIntSet) Add(item int) {
	for _, el := range *s {
		if el == item {
			return
		}
	}
	*s = append(*s, item)
}

func (s orderedIntSet) Subtract(other orderedIntSet) orderedIntSet {
	var diff orderedIntSet
	for _, outer := range s {
		for _, inner := range other {
			if outer == inner {
				goto dontadd
			}
		}
		diff = append(diff, outer)
	dontadd:
	}
	return diff
}

func (s orderedIntSet) Intersection(other orderedIntSet) orderedIntSet {
	// Naive O(n**2) method - don't use on large sets if you care about speed
	var intersection orderedIntSet
	for _, item := range s {
		if other.Contains(item) {
			intersection = append(intersection, item)
		}
	}
	return intersection
}

func (s orderedIntSet) Equal(other orderedIntSet) bool {
	if len(s) != len(other) {
		return false
	}
	// Naive O(n**2) method - don't use on large sets if you care about speed
	for _, item := range s {
		if !other.Contains(item) {
			return false
		}
	}
	return true
}

func (s orderedIntSet) EqualWithOrdering(other orderedIntSet) bool {
	if len(s) != len(other) {
		return false
	}
	// Naive O(n**2) method - don't use on large sets if you care about speed
	for i := range s {
		if s[i] != other[i] {
			return false
		}
	}
	return true
}

func (s orderedIntSet) Union(other orderedIntSet) orderedIntSet {
	// Naive O(n**2) method - don't use on large sets if you care about speed
	var union orderedIntSet
	union = s[:]
	for _, item := range other {
		if !s.Contains(item) {
			union = append(union, item)
		}
	}
	return union
}

func (s orderedIntSet) Min() int {
	var min = math.MaxInt32
	for _, v := range s {
		if v < min {
			min = v
		}
	}
	return min
}

func (s orderedIntSet) Sort() {
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
}

func (s *orderedIntSet) Pop() int {
	x := (*s)[len(*s)-1]
	*s = (*s)[:len(*s)-1]
	return x
}

func (s orderedIntSet) String() string {
	if len(s) == 0 {
		return "[]"
	}
	var rep strings.Builder
	rep.Grow(8 * len(s)) // 6 digits plus a comma and a space
	rep.WriteByte('[')
	lastIdx := len(s) - 1
	for idx, el := range s {
		fmt.Fprintf(&rep, "%d", el)
		if idx != lastIdx {
			rep.WriteString(", ")
		}
	}
	rep.WriteByte(']')
	return rep.String()
}

// fastOrderedIntSet is like orderedIntSet but optimizes for speed at the
// expense of space.
type fastOrderedIntSet struct{ set *orderedset.Set }

type fastOrderedIntSetIt struct{ orderedset.Iterator }

func (x *fastOrderedIntSetIt) Value() int {
	return x.Iterator.Value().(int)
}

func newFastOrderedIntSet(x ...int) *fastOrderedIntSet {
	s := orderedset.New()
	for _, i := range x {
		s.Add(i)
	}
	return &fastOrderedIntSet{s}
}

func (s fastOrderedIntSet) Size() int {
	return s.set.Size()
}

func (s fastOrderedIntSet) Iterator() fastOrderedIntSetIt {
	return fastOrderedIntSetIt{Iterator: s.set.Iterator()}
}

func (s fastOrderedIntSet) Values() []int {
	v := make([]int, s.Size())
	it := s.Iterator()
	for it.Next() {
		v[it.Index()] = it.Value()
	}
	return v
}

func (s fastOrderedIntSet) Contains(x int) bool {
	return s.set.Contains(x)
}

func (s *fastOrderedIntSet) Remove(x int) bool {
	if s.Contains(x) {
		s.set.Remove(x)
		return true
	}
	return false
}

func (s *fastOrderedIntSet) Add(x int) {
	s.set.Add(x)
}

func (s fastOrderedIntSet) Subtract(other *fastOrderedIntSet) *fastOrderedIntSet {
	p := orderedset.New()
	it := s.set.Iterator()
	for it.Next() {
		if !other.set.Contains(it.Value()) {
			p.Add(it.Value())
		}
	}

	return &fastOrderedIntSet{p}
}

func (s fastOrderedIntSet) Intersection(other *fastOrderedIntSet) *fastOrderedIntSet {
	p := orderedset.New()
	it := s.set.Iterator()
	for it.Next() {
		if other.set.Contains(it.Value()) {
			p.Add(it.Value())
		}
	}
	return &fastOrderedIntSet{p}
}

func (s fastOrderedIntSet) Union(other *fastOrderedIntSet) *fastOrderedIntSet {
	p := orderedset.New(s.set.Values()...)
	p.Add(other.set.Values()...)
	return &fastOrderedIntSet{p}
}

func (s fastOrderedIntSet) Sort() *fastOrderedIntSet {
	v := s.set.Values()
	sort.Slice(v, func(i, j int) bool { return v[i].(int) < v[j].(int) })
	return &fastOrderedIntSet{orderedset.New(v...)}
}

func (s fastOrderedIntSet) String() string {
	var b strings.Builder
	b.WriteRune('[')
	it := s.Iterator()
	for it.Next() {
		if it.Index() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.Itoa(it.Value()))
	}
	b.WriteRune(']')
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func readFromProcess(command string) (io.ReadCloser, *exec.Cmd, error) {
	cmd := exec.Command("sh", "-c", command+" 2>&1")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if logEnable(logCOMMANDS) {
		croak("%s: reading from '%s'\n",
			rfc3339(time.Now()), command)
	}
	err = cmd.Start()
	if err != nil {
		return nil, nil, err
	}
	// Pass back cmd so we can call Wait on it and get the error status.
	return stdout, cmd, err
}

func writeToProcess(command string) (io.WriteCloser, *exec.Cmd, error) {
	cmd := exec.Command("sh", "-c", command+" 2>&1")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if logEnable(logCOMMANDS) {
		logit("%s: writing to '%s'\n",
			rfc3339(time.Now()), command)
	}
	err = cmd.Start()
	if err != nil {
		return nil, nil, err
	}
	// Pass back cmd so we can call Wait on it and get the error status.
	return stdout, cmd, err
}

// Either execute a command or raise a fatal exception.
func runProcess(dcmd string, legend string) error {
	if legend != "" {
		legend = " " + legend
	}
	if logEnable(logCOMMANDS) {
		logit("executing '%s'%s", dcmd, legend)
	}
	words, err := shlex.Split(dcmd, true)
	if err != nil {
		return fmt.Errorf("preparing %q for execution: %v", dcmd, err)
	}
	cmd := exec.Command(words[0], words[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("executing %q: %v", dcmd, err)
	}
	return nil
}

// An assumption about line endings: we presently assume that only
// commit comments ever need to be corrected for whether crlf is
// enabled - that is, portions of fast-import streams other than
// comment text always have Unix-style lime endings.

func canonicalizeComment(text string) string {
	text = strings.TrimSpace(text)
	if control.lineSep == "\n" {
		text = strings.Replace(text, "\r\n", control.lineSep, 1)
	} else if control.lineSep == "\r\n" {
		text = strings.Replace(text, "\n", control.lineSep, 1)
	}
	text += control.lineSep
	return text
}

// Importer is capabilities for an import path to some VCS.
// A given VCS can have more than one importer.
type Importer struct {
	name    string    // importer name
	visible bool      // should it be selectable?
	engine  Extractor // Import engine, either a VCS or extractor class
	basevcs *VCS      // Underlying VCS if engine is an extractor
}

var importers []Importer

func init() {
	setInit()
	vcsInit()
	for i := range vcstypes {
		vcs := &vcstypes[i]
		importers = append(importers, Importer{
			name:    vcs.name,
			visible: true,
			engine:  nil,
			basevcs: vcs,
		})
	}
	// Append extractors to this list
	importers = append(importers, Importer{
		name:    "git-extractor",
		visible: false,
		engine:  newGitExtractor(),
		basevcs: findVCS("git"),
	})
	importers = append(importers, Importer{
		name:    "hg-extractor",
		visible: true,
		engine:  newHgExtractor(),
		basevcs: findVCS("hg"),
	})
}

/*
 * Debugging and utility
 *
 * The main point of this design is to make adding and removing log
 * classes simple enough that it can be done ad-hoc for specific
 * debugging missions.  All you need to do to create a new class is
 * add a constant to the iota initializer and a corresponding entry to
 * logtags, then you can use the constant in logit() and logEnable().
 * To remove a class just delete its entry pair.
 */

const (
	logSHOUT      uint = 1 << iota // Errors and urgent messages
	logWARN                        // Exceptional condition, probably not bug
	logBATON                       // Log progress-meter messages, normally only visible for short intervals
	logTAGFIX                      // Log tag fixups
	logTOPOLOGY                    // Log repo-extractor logic (coarse-grained)
	logPROPERTIES                  // Log ignored properties
	logEXTRACT                     // Log repo-extractor logic (fine-grained)
	logFILEMAP                     // Log building of filemaps (warning: very verbose!)
	logANCESTRY                    // Log ancestry computation
	logDELETE                      // Log canonicalization after deletes
	logIGNORES                     // Log ignore generation
	logSVNPARSE                    // Lower-level Subversion parsing details
	logEMAILIN                     // Log round-tripping through msg{out|in}
	logSHUFFLE                     // Log file and directory handling
	logCOMMANDS                    // Show commands as they are executed
	logUNITE                       // Log mark assignments in merging
	logLEXER                       // Log selection-language parsing
)

var logtags = map[string]uint{
	"shout":      logSHOUT,
	"baton":      logBATON,
	"warn":       logWARN,
	"tagfix":     logTAGFIX,
	"topology":   logTOPOLOGY,
	"properties": logPROPERTIES,
	"extract":    logEXTRACT,
	"filemap":    logFILEMAP,
	"ancestry":   logANCESTRY,
	"delete":     logDELETE,
	"ignores":    logIGNORES,
	"svnparse":   logSVNPARSE,
	"emailin":    logEMAILIN,
	"shuffle":    logSHUFFLE,
	"commands":   logCOMMANDS,
	"unite":      logUNITE,
	"lexer":      logLEXER,
}
var optionFlags = [...][2]string{
	{"asciidoc",
		`Dump help items using asciiidoc definition markup.
`},
	{"canonicalize",
		`If set, import stream reads and msgin and edit will canonicalize
comments by replacing CR-LF with LF, stripping leading and trailing whitespace,
and then appending a LF. This behavior inverts if the crlf option is on - LF is
replaced with Cr-LF and CR-LF is appended.
`},
	{"crlf",
		`If set, expect CR-LF line endings on text input and emit them on
output. Comment canonicalization will map LF to CR-LF.
`},
	{"compress",
		`Use compression for on-disk copies of blobs. Accepts an increase
in repository read and write time in order to reduce the amount of
disk space required while editing; this may be useful for large
repositories. No effect if the edit input was a dump stream; in that
case, reposurgeon doesn't make on-disk blob copies at all (it points
into sections of the input stream instead).
`},
	{"echo",
		`Echo commands before executing them. Setting this in test scripts may 
make the output easier to read.
`},
	{"experimental",
		`This flag is reserved for developer use.  If you set it, it could do
anything up to and including making demons fly out of your nose.
`},
	{"interactive",
		`Enable interactive responses even when not on a tty.
`},
	{"progress",
		`Enable fancy progress messages even when not on a tty.
`},
	{"relax",
		`Continue script execution on error, do not bail out.
`},
	{"serial",
		`Disable parallelism in code. Use for generating test loads.
`},
	{"testmode",
		`Disable some features that cause output to be vary depending on wall time,
screen width, and the ID of the invoking user. Use in regression-test loads.
`},
	{"quiet",
		`Suppress time-varying parts of reports.
`},
}

// innerControl is all the control-block stuff used by this module.
type innerControl struct {
	lineSep     string
	blobseq     blobidx
	flagOptions map[string]bool
	readLimit   uint64
}

// whoami - ask various programs that keep track of who you are
func whoami() (string, string) {
	if control.flagOptions["testmode"] {
		return "Fred J. Foonly", "foonly@foo.com"
	}

	name, email, err := fqme.WhoAmI()
	if err == nil {
		return name, email
	}

	// Out of alternatives
	log.Fatal("can't deduce user identity!")
	return "", ""
}

// Utility classes

// emptyComment says whether comment info should be considered disposable?
func emptyComment(c string) bool {
	if c == "" {
		return true
	}
	c = strings.TrimSpace(c)
	// A CVS artifact from ancient times.
	if c == "" || c == "*** empty log message ***" {
		return true
	}
	return false
}

// OrderedMap is a map with preserved key order
type OrderedMap struct {
	keys      []string
	dict      map[string]string
	valueLess func(string, string) bool
}

func newOrderedMap() OrderedMap {
	d := new(OrderedMap)
	d.keys = make([]string, 0)
	d.dict = make(map[string]string)
	return *d
}

func copyOrderedMap(source *OrderedMap) *OrderedMap {
	d := new(OrderedMap)
	d.keys = make([]string, len(source.keys))
	d.dict = make(map[string]string, len(source.dict))
	for i, k := range source.keys {
		d.keys[i] = k
		d.dict[k] = source.dict[k]
	}
	return d
}

func (d *OrderedMap) get(hd string) string {
	payload, ok := d.dict[hd]
	if !ok {
		return ""
	}
	return payload
}

func (d *OrderedMap) has(hd string) bool {
	_, ok := d.dict[hd]
	return ok
}

func (d *OrderedMap) set(hd string, data string) {
	d.dict[hd] = data
	for _, k := range d.keys {
		if hd == k {
			return
		}
	}
	d.keys = append(d.keys, hd)
}

func (d *OrderedMap) delete(hd string) bool {
	delete(d.dict, hd)
	for i, el := range d.keys {
		if hd == el {
			// Zero out the deleted element so it's GCed
			copy(d.keys[i:], d.keys[i+1:])
			d.keys[len(d.keys)-1] = ""
			d.keys = d.keys[:len(d.keys)-1]
			return true
		}
	}
	return false
}

func (d OrderedMap) Len() int {
	return len(d.keys)
}

// Clear allows the storage in this map to be GCed
func (d *OrderedMap) Clear() {
	d.keys = nil
	d.dict = nil
}

func (d OrderedMap) String() string {
	var rep strings.Builder
	rep.WriteByte('{')
	lastIdx := d.Len() - 1
	for idx, el := range d.keys {
		fmt.Fprintf(&rep, "'%s': '%s'", el, d.dict[el])
		if idx != lastIdx {
			rep.WriteString(", ")
		}
	}
	rep.WriteByte('}')
	return rep.String()
}

func (d OrderedMap) vcString() string {
	var out = "{"
	for _, el := range d.keys {
		val := d.dict[el]
		for _, vcs := range vcstypes {
			if val == vcs.dfltignores {
				val = "{{" + vcs.name + "-defaults}}"
				break
			}
		}
		out += "'" + el + "': '" + val + "', "
	}
	if len(d.keys) > 0 {
		out = out[:len(out)-2]
	}
	return out + "}"
}

// Less returns true if the sort method says the value for key i is
// less than the value for key j. Useful with the Go library sort.
func (d OrderedMap) Less(i int, j int) bool {
	return d.valueLess(d.dict[d.keys[i]], d.dict[d.keys[j]])
}

// Swap interchanges key i and j. Useful with the Go library sort.
func (d OrderedMap) Swap(i int, j int) {
	keep := d.keys[j]
	d.keys[j] = d.keys[i]
	d.keys[i] = keep
}

/*
 * RFC2822 message blocks
 *
 * This is how we serialize comments so they can be modified in an editor.
 */

// MessageBlockDivider is the separator between messages in a message-box.
var MessageBlockDivider = bytes.Repeat([]byte("-"), 78)

// MessageBlock is similar to net/mail's type, but the body is pulled inboard
// as a string.  This is appropriate because change comments are normally short.
type MessageBlock struct {
	hdnames orderedStringSet
	header  map[string]string
	body    string
}

// newMessageBlock is like net/mail ReadMessage but with a special delimiter.
// Also, we preserve the order of headers read in.
func newMessageBlock(bp *bufio.Reader) (*MessageBlock, error) {
	msg := new(MessageBlock)
	msg.hdnames = make([]string, 0)
	msg.header = make(map[string]string)

	isLineEnder := func(data string) bool {
		// Second case handles WSL
		return data == "\n" || data == "\r\n"
	}

	headerize := func(data string) bool {
		if isLineEnder(data) {
			return false
		}
		colon := strings.Index(data, ":")
		if colon == -1 {
			panic(throw("msgbox", "Ill-formed line %q in mail message", data))
		}
		key := data[0:colon]
		payload := strings.TrimSpace(data[colon+1:])
		msg.hdnames = append(msg.hdnames, key)
		msg.header[key] = payload
		return true
	}

	if bp != nil {
		inBody := false
		firstline, err := bp.ReadBytes('\n')
		if err != nil {
			return nil, err
		} else if !bytes.HasPrefix(firstline, MessageBlockDivider) {
			headerize(string(firstline))
		}
		for {
			line, err := bp.ReadBytes('\n')
			if err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			if !inBody && (line[0] == ' ' || line[0] == '\t') {
				// RFC2822 continuation
				if len(msg.hdnames) >= 1 {
					lasthdr := msg.hdnames[len(msg.hdnames)-1]
					msg.setHeader(lasthdr,
						msg.getHeader(lasthdr)+string(line))
				}
			} else if !inBody && !headerize(string(line)) {
				inBody = true
				continue
			} else if inBody {
				// check for delimiter
				if bytes.HasPrefix(line, MessageBlockDivider) {
					break
				}
				// undo byte-stuffing *after* the delimiter check
				if bytes.HasPrefix(line, []byte(".")) {
					line = line[1:len(line)]
				}
				msg.body += string(line)
			}
		}

		return msg, err
	}

	return msg, nil
}

func (msg *MessageBlock) getPayload() string {
	return msg.body
}

func (msg *MessageBlock) setPayload(data string) {
	msg.body = data
}

func (msg *MessageBlock) getHeader(hd string) string {
	hdrs, ok := msg.header[hd]
	if !ok {
		return ""
	}
	return hdrs
}

func (msg *MessageBlock) setHeader(hd string, data string) {
	msg.header[hd] = string(data)
	msg.hdnames = append(msg.hdnames, hd)
}

func (msg *MessageBlock) deleteHeader(hd string) {
	delete(msg.header, hd)
	msg.hdnames.Remove(hd)
}

func (msg *MessageBlock) filterHeaders(regexp *regexp.Regexp) {
	// because iterator invalidation
	tmp := make([]string, len(msg.hdnames))
	copy(tmp, msg.hdnames)
	for _, key := range tmp {
		if !regexp.MatchString(key + ":") {
			msg.deleteHeader(key)
		}
	}
}

func (msg *MessageBlock) String() string {
	var b strings.Builder
	fmt.Fprintln(&b, string(MessageBlockDivider))
	for _, k := range msg.hdnames {
		if v := msg.header[k]; v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	b.WriteByte('\n')
	scanner := bufio.NewScanner(strings.NewReader(msg.body))
	for scanner.Scan() {
		line := scanner.Text()
		// byte stuffing so we can protect instances of the delimiter
		// within message bodies.
		if strings.HasPrefix(line, ".") || strings.HasPrefix(line, string(MessageBlockDivider)) {
			b.WriteByte('.')
		}
		fmt.Fprintln(&b, line)
	}
	return b.String()
}

/*
 * Time, date, and zone handling
 */

var isocodeToZone = make(map[string]string)

// zoneFromEmail attempts to deduce an IANA time zone from an email address.
// Only works when the TLD is an ISO country code that has exactly one entry
// in the IANA timezone database; it's a big fail for com/edu/org/net and
// big countries like the US.
func zoneFromEmail(addr string) string {
	if len(isocodeToZone) == 0 {
		file, err := os.Open("/usr/share/zoneinfo/zone.tab")
		if err != nil {
			croak("no country-code to timezone mapping")
		} else {
			defer file.Close()

			firstpass := make(map[string][]string)

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, "#") {
					continue
				}
				fields := strings.Fields(line)
				code := strings.ToLower(fields[0])
				zone := fields[2]
				_, ok := firstpass[code]
				if !ok {
					firstpass[code] = make([]string, 0)
				}
				firstpass[code] = append(firstpass[code], zone)
			}
			for k, v := range firstpass {
				if len(v) == 1 {
					isocodeToZone[k] = v[0]
				}
			}

			if err := scanner.Err(); err != nil {
				log.Fatal(err)
			}
		}
	}

	fields := strings.Split(addr, ".")
	toplevel := fields[len(fields)-1]

	// If the top-level domain is an ISO country code that implies a
	// single IANA timezine, we're good.
	return isocodeToZone[toplevel]
}

// rfc3339 makes a UTC RFC3339 string from a system timestamp.
// Go's format rules document that this will end with Z, not an 00:00 timezone.
func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

var gitDateRE = regexp.MustCompile(`^[0-9]+\s*[+-][0-9]+$`)
var zoneOffsetRE = regexp.MustCompile(`^([-+]?[0-9]{2})([0-9]{2})$`)

// locationFromZoneOffset makes a Go location object from a [+-]hhhmmm string.
// It is rather a strained hack. We don't get an actual TZ from a
// Git-format date, just a [+-]hhmm offset. Fortunately that
// is usually all we need to dump. Make a location from which
// we can get back the offset string, by storing it as the zone name.
func locationFromZoneOffset(offset string) (*time.Location, error) {
	m := zoneOffsetRE.FindSubmatch([]byte(offset))
	if m == nil || len(m) != 3 {
		return nil, errors.New("ill-formed timezone offset " + string(offset))
	}
	hours, _ := strconv.Atoi(string(m[1]))
	mins, _ := strconv.Atoi(string(m[2]))
	if hours < -14 || hours > 13 || mins > 59 {
		// According to RFC2822/RFC1123 we could put "-0000" in here to
		// indicate invalid zone information.
		return nil, errors.New("dubious zone offset " + string(offset))
	}
	tzoff := (hours*60 + mins) * 60
	return time.FixedZone(offset, tzoff), nil
}

// Date wraps a system time object, giving it various serialization and
// deserialization capabilities.
type Date struct {
	timestamp time.Time
}

// GitLogFormat - which git falsely claims is RFC2822-conformant.
// In reality RFC2822 would be "Mon, 02 Aug 2006 15:04:05 -0700",
// which is Go's RFC1123Z format. (We're ignoring obsolete variants
// with letter zones and two-digit years.)
const GitLogFormat = "Mon Jan 02 15:04:05 2006 -0700"

// RFC1123ZNoComma is the swapped format
const RFC1123ZNoComma = "Mon 02 Jan 2006 15:04:05 -0700"

// newDate exists mainly to wrap a parser to recognize date formats that
// exporters or email programs might emit
func newDate(text string) (Date, error) {
	var t Date
	// Special case: we may want current time in UTC
	if len(text) == 0 {
		t.timestamp = time.Now().UTC()
		return t, nil
	}
	// Otherwise, look for git's preferred format, which is a timestamp
	// in UTC followed by an offset to be used as a hint for what
	// timezone to display the date in when converting to other
	// formats
	text = strings.TrimSpace(text)
	if gitDateRE.Find([]byte(text)) != nil {
		fields := strings.Fields(text)
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			return t, err
		}
		loc, err2 := locationFromZoneOffset(fields[1])
		if err2 != nil {
			return t, err2
		}
		t.timestamp = time.Unix(int64(n), 0).In(loc)
		return t, nil

	}
	// RFC3339 - because it's the presentation format I prefer
	// RFC1123Z - we use it in message-block headers
	// GitLog - git log emits this format
	for _, layout := range []string{time.RFC3339, time.RFC1123Z, GitLogFormat, RFC1123ZNoComma} {
		trial, err3 := time.Parse(layout, text)
		if err3 == nil {
			// Could be Round() rather than Truncate() - it's this way
			// for compatibility with the ancestral Python.
			t.timestamp = trial.Truncate(1 * time.Second)
			return t, nil
		}
	}
	return t, errors.New("not a valid timestamp: " + string(text))
}

// isZero tells us if this is an uninitialized date object
func (date *Date) isZero() bool {
	return date.timestamp.IsZero()
}

func (date *Date) clone() Date {
	out := *date
	return out
}

func (date Date) rfc3339() string {
	return rfc3339(date.timestamp)
}

func (date Date) gitlog() string {
	return date.timestamp.Format(GitLogFormat)
}

func (date Date) rfc1123() string {
	// Alas, the format Go calls RFC822 is archaic
	return date.timestamp.Format(time.RFC1123Z)
}

func (date Date) delta(other Date) time.Duration {
	return other.timestamp.Sub(date.timestamp)
}

const dayHalf = (24 * 60 * 60) / 2

// String formats a Date object as an internal Git date (Unix time in seconds
// and a hhmm offset).
func (date Date) String() string {
	return fmt.Sprintf("%d %s", date.timestamp.Unix(), date.timestamp.Format("-0700"))
}

func (date *Date) setTZ(zone string) {
	loc, err := time.LoadLocation(zone)
	if err == nil {
		date.timestamp = date.timestamp.In(loc)
	}
}

// Equal tests equality of Date objects
func (date *Date) Equal(other Date) bool {
	return date.timestamp.Equal(other.timestamp)
}

// Before tests time ordering of Date objects
func (date *Date) Before(other Date) bool {
	return date.timestamp.Before(other.timestamp)
}

// After tests rime ordering of Date objects
func (date *Date) After(other Date) bool {
	return date.timestamp.After(other.timestamp)
}

/*
 * Attributions
 */

// Attribution pins a repository event to a person and time.
type Attribution struct {
	fullname string
	email    string
	date     Date
}

var attributionRE = regexp.MustCompile(`([^<]*\s*)<([^>]*)>+(\s*.*)`)

// parseAttributionLine parses a Git attribution line into its fields
func parseAttributionLine(line string) (string, string, string, error) {
	m := attributionRE.FindSubmatch(bytes.TrimSpace([]byte(line)))
	if m != nil {
		name := string(bytes.TrimSpace(m[1]))
		address := string(bytes.TrimSpace(m[2]))
		date := string(bytes.TrimSpace(m[3]))
		return name, address, date, nil
	}
	err := fmt.Errorf("malformed attribution on '%s'", line)
	return "", "", "", err
}

func (attr *Attribution) address() (string, string) {
	return attr.fullname, attr.email
}

// newAttribution makes an Attribution from an author or committer line
func newAttribution(attrline string) (*Attribution, error) {
	attr := new(Attribution)

	if attrline != "" {
		fullname, email, datestamp, err1 := parseAttributionLine(attrline)
		if err1 != nil {
			return attr, fmt.Errorf("malformed attribution: %v", err1)
		}
		parsed, err2 := newDate(datestamp)
		if err2 != nil {
			return attr,
				fmt.Errorf("malformed attribution date '%s' in '%s': %v",
					datestamp, attrline, err2)
		}
		// Deal with a cvs2svn artifact
		if fullname == "(no author)" {
			fullname = "no-author"
		}
		attr.fullname = fullname
		attr.email = email
		attr.date = parsed
	}
	return attr, nil
}

func (attr Attribution) String() string {
	return attr.fullname + " <" + attr.email + "> " + attr.date.String()
}

func (attr *Attribution) clone() *Attribution {
	mycopy, _ := newAttribution("")
	mycopy.fullname = attr.fullname
	mycopy.email = attr.email
	mycopy.date = attr.date.clone()
	return mycopy
}

func (attr Attribution) isEmpty() bool {
	return attr.fullname == "" && attr.email == ""
}

// emailOut updates a message-block object with a representation of this
// attribution object.
func (attr *Attribution) emailOut(modifiers orderedStringSet, msg *MessageBlock, hdr string) {
	msg.setHeader(hdr, attr.fullname+" <"+attr.email+">")
	msg.setHeader(hdr+"-Date", attr.date.rfc1123())
}

// Equal compares attributions
func (attr *Attribution) Equal(other *Attribution) bool {
	return attr.fullname == other.fullname && attr.email == other.email && attr.date.Equal(other.date)
}

func (attr *Attribution) actionStamp() string {
	return attr.date.rfc3339() + "!" + attr.email
}

func (attr *Attribution) userid() string {
	id, _ := splitRuneFirst(attr.email, '@')
	return id
}

func (attr *Attribution) who() string {
	return attr.fullname + " <" + attr.email + ">"
}

// Remap changes the attribution fullname/email according to a map of author entries.
func (attr *Attribution) remap(authors map[string]Contributor) bool {
	matches := func(attr *Attribution, local string, ae Contributor) bool {
		nlower := strings.ToLower(attr.fullname)
		elower := strings.ToLower(attr.email)
		return strings.HasPrefix(elower, local+"@") || elower == local || (attr.email == "" && nlower == local)
	}

	changed := false
	for local, ae := range authors {
		if matches(attr, local, ae) {
			if attr.fullname != ae.fullname || attr.email != ae.email {
				changed = true
			}
			attr.fullname = ae.fullname
			attr.email = ae.email
			if ae.timezone != "" {
				attr.date.setTZ(ae.timezone)
			}
			break
		}
	}
	return changed
}

/*
 * Hashing.  These functions are the only place in the code
 * that knows what hash Git actually uses.  Elsewhere hashes
 * are treated as uninterpreted cookies that can be formatted
 * as hex-quad pairs of an unspecified length.
 *
 * However, because the gitHash functions on Blob and Commit
 * objects depend on using these hashes internally there might
 * be a dependency there.
 *
 * The way hash computation works is a bit tricky in order to
 * do the least work possible. On a read from a live repository
 * the original-oid field of the export stream is interpreted
 * so we don't have to do any hash computation.  On a read from
 * a stream that does not include OIDs, the hash slots are left
 * empty (invalid). Whenever a hash of an object is called for,
 * the stored value is used if valid; otherwise the hash is computed,
 * stored, and returned. Computation can trigger a cascade of hash
 * computations back to the root.
 *
 * Finally, whenever a blob or commit is modified its hash slot is
 * invalidated.  This is easy to guarantee with blobs because there
 * is only one method through which their content is altered. Commits
 * are a different matter; in practice it's not easy to be sure of all
 * the places where a commit is modified, and if we ever see buggy
 * behavior around hashes it would be wise to suspect that there is
 * a missing invalidation call somewhere.
 */
type gitHashType [sha1.Size]byte

var nullGitHash gitHashType // Do not modify this!

func newGitHash(b []byte) gitHashType {
	var h gitHashType
	if b != nil {
		fmt.Sscan(string(b), "%x", &h)
	}
	return h
}

func gitHashString(data string) gitHashType {
	return sha1.Sum([]byte(data))
}

func (h gitHashType) hexify() string {
	return fmt.Sprintf("%040x", h)
}

func (h gitHashType) isValid() bool {
	return h != nullGitHash
}

func (h *gitHashType) invalidate() {
	*h = nullGitHash
}

func (h gitHashType) short() string {
	return h.hexify()[:6]
}

/*
 * Repository objects.  All satisfy the Event interface.
 */

// Cookie describes a VCS magic cookie embedded in a source file.
type Cookie struct {
	path string
	rev  string
}

func (c Cookie) isEmpty() bool {
	return c.path == "" && c.rev == ""
}

func (c Cookie) implies() string {
	// This is historically dubious - could be RCS, but there's no way
	// to distinguish that from CVS
	if strings.Contains(c.rev, ".") {
		return "CVS"
	}
	return "svn"
}

const emptyMark = markidx(0)
const maxMark = ^markidx(0)

func (m markidx) String() string {
	if m == emptyMark {
		return ""
	}
	return fmt.Sprintf(":%d", m)
}

func markNumber(markstring string) markidx {
	n, _ := strconv.ParseUint(markstring[1:], 10, bits.UintSize)
	return markidx(n & uint64(^markidx(0)))
}

//func intToMarkidx(markint uint) markidx {
//	return markidx(markint & uint(^markidx(0)))
//}

// Blob represents a detached blob of data referenced by a mark.
type Blob struct {
	mark      string
	abspath   string
	cookie    *Cookie // CVS/SVN cookie analyzed out of this file
	repo      *Repository
	opset     map[*FileOp]bool // Fileops associated with this blob
	opsetLock sync.Mutex
	start     int64 // Seek start if this blob refers into a dump
	size      int64 // length start if this blob refers into a dump
	blobseq   blobidx
	hash      gitHashType
	colors    colorSet // Scratch space for graph-coloring algorithms
}

const noOffset = -1

func newBlob(repo *Repository) *Blob {
	b := new(Blob)
	b.repo = repo
	b.opset = make(map[*FileOp]bool)
	b.start = noOffset
	b.blobseq = control.blobseq
	control.blobseq++
	if control.blobseq == ^blobidx(0) {
		panic("blob index overflow, rebuild with a larger size")
	}
	return b
}

func (b Blob) getDelFlag() bool {
	return b.colors.Contains(colorDELETE)
}

func (b *Blob) setDelFlag(t bool) {
	if t {
		b.colors.Add(colorDELETE)
	} else {
		b.colors.Remove(colorDELETE)
	}
}

// idMe IDs this blob for humans.
func (b *Blob) idMe() string {
	return fmt.Sprintf("blob@%s", b.mark)
}

// paths is implemented for uniformity with commits and fileops."
func (b *Blob) paths(_pathtype orderedStringSet) orderedStringSet {
	lst := newOrderedStringSet()
	seen := make(map[string]bool)
	for op := range b.opset {
		// The fileop is necessarily a M fileop
		if !seen[op.Path] {
			lst = append(lst, op.Path)
			seen[op.Path] = true
		}
	}
	sort.Strings(lst)
	return lst
}

func (b *Blob) appendOperation(op *FileOp) {
	b.opsetLock.Lock()
	b.opset[op] = true
	b.opsetLock.Unlock()
	b.hash.invalidate()
}

func (b *Blob) removeOperation(op *FileOp) bool {
	b.opsetLock.Lock()
	delete(b.opset, op)
	b.opsetLock.Unlock()
	b.hash.invalidate()
	return len(b.opset) > 0
}

func (b *Blob) setBlobfile(argpath string) {
	file, _ := os.Open(argpath)
	info, _ := file.Stat()
	b.size = info.Size()
	b.abspath = argpath
	b.hash.invalidate()
}

// getBloobfile returns the path where the blob's content lives.
func (b *Blob) getBlobfile(create bool) string {
	if b.abspath != "" {
		return b.abspath
	}
	stem := fmt.Sprintf("%09d", b.blobseq)
	// The point of the breaking up the ID into multiple sections is to use
	// the filesystem to speed up lookup time.
	parts := strings.Split(filepath.FromSlash(b.repo.subdir("")), "/")
	parts = append(parts,
		[]string{"blobs", stem[0:3], stem[3:6], stem[6:]}...)
	if create {
		dir := strings.Join(parts[0:len(parts)-1], "/")
		err := os.MkdirAll(filepath.FromSlash(dir), userReadWriteSearchMode)
		if err != nil {
			panic(fmt.Errorf("Blob creation: %v", err))
		}
	}
	return filepath.FromSlash(strings.Join(parts[0:], "/"))
}

// hasfile answers the question: "Does this blob have its own file?"
func (b *Blob) hasfile() bool {
	return b.repo.seekstream == nil || b.start == noOffset
}

// getContent gets the content of the blob as a string.
func (b *Blob) getContent() []byte {
	if !b.hasfile() {
		var data = make([]byte, b.size)
		_, err := b.repo.seekstream.ReadAt(data, b.start)
		if err != nil {
			panic(fmt.Errorf("Blob fetch: %v", err))
		}
		return data
	}
	var data []byte
	file, err := os.Open(b.getBlobfile(false))
	if err != nil {
		panic(fmt.Errorf("Blob read: %v", err))
	}
	defer file.Close()
	if control.flagOptions["compress"] {
		input, err2 := gzip.NewReader(file)
		if err2 != nil {
			panic(err.Error())
		}
		defer input.Close()
		data, err = ioutil.ReadAll(input)
	} else {
		data, err = ioutil.ReadAll(file)
	}
	if err != nil {
		panic(fmt.Errorf("Blob read: %v", err))
	}
	return data
}

type sectionReader struct {
	*io.SectionReader
}

func newSectionReader(r io.ReaderAt, off int64, n int64) *sectionReader {
	return &sectionReader{io.NewSectionReader(r, off, n)}
}
func (sr sectionReader) Close() error {
	return nil
}

// getContentStream gets the content of the blob as a Reader.
func (b *Blob) getContentStream() io.ReadCloser {
	if !b.hasfile() {
		return newSectionReader(b.repo.seekstream, b.start, b.size)
	}
	file, err := os.Open(b.getBlobfile(false))
	if err != nil {
		panic(fmt.Errorf("Blob read: %v", err))
	}
	if control.flagOptions["compress"] {
		input, err2 := gzip.NewReader(file)
		if err2 != nil {
			panic(err.Error())
		}
		return input
	}
	return file
}

// setContent sets the content of the blob from a string.
// tell is the start offset of the data in the input source;
// if it is noOffset, there is no seek stream and creation of
// an on-disk blob is forced.
func (b *Blob) setContent(text []byte, tell int64) {
	b.start = tell
	b.size = int64(len(text))
	if b.hasfile() {
		file, err := os.OpenFile(b.getBlobfile(true),
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC, userReadWriteMode)
		if err != nil {
			panic(fmt.Errorf("Blob write: %v", err))
		}
		defer file.Close()
		if control.flagOptions["compress"] {
			output := gzip.NewWriter(file)

			defer output.Close()
			_, err = output.Write(text)
		} else {
			_, err = file.Write(text)
		}
		if err != nil {
			panic(fmt.Errorf("Blob writer: %v", err))
		}
	}
}

// setContentFromStream sets the content of the blob from a reader stream.
func (b *Blob) setContentFromStream(s io.ReadCloser) {
	// maybe the caller should close it?
	defer s.Close()
	b.start = noOffset
	file, err := os.OpenFile(b.getBlobfile(true),
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, userReadWriteMode)
	if err != nil {
		panic(fmt.Errorf("Blob write: %v", err))
	}
	defer file.Close()
	var nBytes int64
	if control.flagOptions["compress"] {
		output := gzip.NewWriter(file)

		defer output.Close()
		nBytes, err = io.Copy(output, s)
	} else {
		nBytes, err = io.Copy(file, s)
	}
	if err != nil {
		panic(fmt.Errorf("Blob writer: %v", err))
	}
	b.size = nBytes
	b.hash.invalidate()
}

// materialize stores this content as a separate file, if it isn't already.
func (b *Blob) materialize() string {
	if b.start != noOffset {
		content := b.getContentStream()
		defer content.Close()
		b.setContentFromStream(content)
	}
	return b.getBlobfile(false)
}

// what to treat as a coment when message-boxing
func (b Blob) getComment() string {
	return string(b.getContent())
}

// getMark returns the blob's identifying mark
func (b Blob) getMark() string {
	return b.mark
}

// setMark sets the blob's mark
func (b *Blob) setMark(mark string) string {
	if b.repo != nil {
		b.repo.fixupMarkToIndex(b, b.mark, mark)
	}
	b.mark = mark
	return mark
}

// forget de-links this commit from its repo.
func (b *Blob) forget() {
	b.repo = nil
}

func (b Blob) isCommit() bool {
	return false
}

// moveto changes the repo this blob is associated with."
func (b *Blob) moveto(repo *Repository) {
	if b.hasfile() {
		// the relpath calls are fir readabiliyu if we error out
		oldloc := relpath(b.getBlobfile(false))
		b.repo = repo
		newloc := relpath(b.getBlobfile(true))
		if logEnable(logSHUFFLE) {
			logit("blob moveto calls os.rename(%s, %s)", oldloc, newloc)
		}
		err := os.Rename(oldloc, newloc)
		if err != nil {
			panic(err)
		}
		b.hash.invalidate()
	}
}

// clone makes a fresh (uncolored) copy of this blob, pointing at the same file."
func (b *Blob) clone(repo *Repository) *Blob {
	c := b // copy scalar fields
	c.opsetLock.Lock()
	c.opset = make(map[*FileOp]bool, len(b.opset))
	for op := range b.opset {
		c.opset[op] = true
	}
	c.opsetLock.Unlock()
	c.colors.Clear()
	if b.hasfile() {
		// the relpath calls are fir readabiliyu if we error out
		bpath := relpath(b.getBlobfile(false))
		cpath := relpath(c.getBlobfile(false))
		if logEnable(logSHUFFLE) {
			logit("blob clone for %s calls os.Link(): %s -> %s", b.mark, bpath, cpath)
		}
		err := os.Link(bpath, cpath)
		if err != nil {
			panic(fmt.Errorf("Blob clone: %v", err))
		}
	} else {
		if logEnable(logSHUFFLE) {
			logit("%s blob %s is not materialized.", repo.name, b.mark)
		}
	}
	b.hash.invalidate()
	return c
}

func (b *Blob) gitHash() gitHashType {
	if !b.hash.isValid() {
		content := b.getContent()
		b.hash = gitHashString(fmt.Sprintf("blob %d\x00", len(content)) + string(content))
	}
	return b.hash
}

// Examples of embedded VCS headers:
// RCS, CVS: $Revision: 1.4 $
// SVN:      $Revision: 144 $
// RCS, CVS: $Id: lofs.h,v 1.8 1992/05/30 10:05:43 jsp Exp jsp $
// SVN:      $Id: calc.c 148 2006-07-28 21:30:43Z sally $
// Note that the SVN formats are distinguished from the CVS ones by the
// absence of a dot in the revision.  The Subversion book says Revision
// can also appear under the alias "Rev" or "LastChangedRev".
var dollarID = regexp.MustCompile(`\$Id *: *([^$]+) *\$`)
var dollarRevision = regexp.MustCompile(`\$Rev(?:ision) *: *([^$]*) *\$`)
var dollarLastChanged = regexp.MustCompile(`\$LastChangedRev *: *([^$]*) *\$`)

func (b *Blob) parseCookie(content string) *Cookie {
	// Parse CVS && Subversion $-headers
	// There'd better not be more than one of these per blob.
	var cookie Cookie
	for _, m := range dollarID.FindAllStringSubmatch(content, 0) {
		fields := strings.Fields(m[0])
		if len(fields) == 2 {
			if strings.HasSuffix(fields[1], ",v") {
				cookie.path = fields[1][:len(fields[1])-2]
			} else {
				cookie.path = stringCopy(fields[1])
			}
			cookie.rev = stringCopy(fields[2])
		}
	}
	for _, m := range dollarRevision.FindAllStringSubmatch(content, 0) {
		cookie.rev = stringCopy(strings.TrimSpace(m[1]))
	}
	for _, m := range dollarLastChanged.FindAllStringSubmatch(content, 0) {
		cookie.rev = stringCopy(strings.TrimSpace(m[1]))
	}
	if cookie.isEmpty() {
		return nil
	}
	b.cookie = &cookie
	return b.cookie
}

// Save this blob in import-stream format without constructing a string
func (b *Blob) Save(w io.Writer) {
	if b.hasfile() {
		fn := b.getBlobfile(false)
		if !exists(fn) {
			return
		}
	}
	content := b.getContentStream()
	defer content.Close()
	fmt.Fprintf(w, "blob\nmark %s\n", b.mark)
	if b.hash.isValid() {
		fmt.Fprintf(w, "original-oid %s\n", b.hash.hexify())
	}
	fmt.Fprintf(w, "data %d\n", b.size)
	io.Copy(w, content)
	w.Write([]byte{'\n'})
}

// String serializes this blob into a string
func (b Blob) String() string {
	var bld strings.Builder
	b.Save(&bld)
	return bld.String()
}

// emailOut enables DoMsgout() to report blobs, if requested with --blobs.
func (b *Blob) emailOut(modifiers orderedStringSet,
	eventnum int, filterRegexp *regexp.Regexp) string {
	msg, _ := newMessageBlock(nil)
	msg.setHeader("Event-Number", fmt.Sprintf("%d", eventnum+1))
	msg.setHeader("Event-Mark", b.mark)
	msg.setPayload(b.getComment())

	if filterRegexp != nil {
		msg.filterHeaders(filterRegexp)
	}

	return msg.String()
}

// emailIn updates this blob from a parsed email message.
func (b *Blob) emailIn(msg *MessageBlock, fill bool) bool {
	modified := false
	newcontent := msg.getPayload()
	if newcontent != b.getComment() {
		if logEnable(logEMAILIN) {
			logit("in %s, content is modified %q -> %q", b.idMe(), b.getComment(), newcontent)
		}
		modified = true
		b.setContent([]byte(newcontent), noOffset)
		b.hash.invalidate()
	}
	return modified
}

// Tag describes a a gitspace annotated tag object
type Tag struct {
	repo       *Repository
	tagname    string
	committish string
	tagger     *Attribution
	Comment    string
	legacyID   string
	color      colorType
}

func newTag(repo *Repository,
	name string, committish string,
	tagger *Attribution, comment string) *Tag {
	t := new(Tag)
	if strings.HasPrefix(name, "refs/tags/") {
		t.tagname = name[10:]
	} else {
		t.tagname = name
	}
	t.tagger = tagger
	t.Comment = comment
	t.remember(repo, committish)
	return t
}

func (t Tag) getDelFlag() bool {
	return t.color == colorDELETE
}

func (t *Tag) setDelFlag(b bool) {
	if b {
		t.color = colorDELETE
	} else {
		t.color = colorNONE
	}
}

// getMark returns the tag's identifying mark
// Not actually used, needed to satisfy Event interface
func (t Tag) getMark() string {
	return ""
}

// remember records an attachment to a repo and commit.
func (t *Tag) remember(repo *Repository, committish string) {
	t.repo = repo
	t.committish = committish
	if repo != nil {
		if event := repo.markToEvent(committish); event != nil {
			event.(*Commit).attach(t)
		}
	}
}

// forget removes this tag's attachment to its commit and repo.
func (t *Tag) forget() {
	if event := t.repo.markToEvent(t.committish); event != nil {
		event.(*Commit).detach(t)
	}
	t.repo = nil
}

// moveto changes the repo this tag is associated with."
func (t *Tag) moveto(repo *Repository) {
	t.repo = repo
}

// index returns our 0-origin index in our repo.
func (t *Tag) index() int {
	return t.repo.eventToIndex(t)
}

// getComment returns the comment attached to a tag
func (t Tag) getComment() string { return t.Comment }

// idMe IDs this tag for humans."
func (t *Tag) idMe() string {
	suffix := ""
	if t.legacyID != "" {
		suffix = "=<" + t.legacyID + ">"
	}
	return fmt.Sprintf("tag@%s%s (%s)", t.committish, suffix, t.tagname)
}

// actionStamp controls how a tag stamp is made
func (t *Tag) actionStamp() string {
	return t.tagger.actionStamp()
}

// showlegacy yields a legacy ID in the expected form for the ancestral system.
func (t *Tag) showlegacy() string {
	if t.legacyID == "" {
		return ""
	}
	// Special case for Subversion
	if t.repo != nil && t.repo.vcs != nil && t.repo.vcs.name == "svn" {
		return "r" + t.legacyID
	}
	return t.legacyID
}

// tags enables DoTags() to report tags.
func (t *Tag) tags(modifiers orderedStringSet, eventnum int, _cols int) string {
	return fmt.Sprintf("%6d\ttag\t%s", eventnum+1, t.tagname)
}

// emailOut enables DoMsgout() to report tag metadata
func (t *Tag) emailOut(modifiers orderedStringSet, eventnum int,
	filterRegexp *regexp.Regexp) string {
	msg, _ := newMessageBlock(nil)
	msg.setHeader("Event-Number", fmt.Sprintf("%d", eventnum+1))
	msg.setHeader("Tag-Name", t.tagname)
	msg.setHeader("Target-Mark", t.committish)
	if t.tagger != nil {
		t.tagger.emailOut(modifiers, msg, "Tagger")
	}
	if t.legacyID != "" {
		msg.setHeader("Legacy-ID", t.legacyID)
	}
	check, _ := splitRuneFirst(t.Comment, '\n')
	if len(check) > 64 {
		check = check[0:64]
	}
	msg.setHeader("Check-Text", check)
	msg.setPayload(t.Comment)
	if t.Comment != "" && !strings.HasSuffix(t.Comment, "\n") && logEnable(logWARN) {
		logit("in tag %s, comment was not LF-terminated.", t.tagname)
	}
	if filterRegexp != nil {
		msg.filterHeaders(filterRegexp)
	}
	return msg.String()
}

// emailIn updates this Tag from a parsed message block.
func (t *Tag) emailIn(msg *MessageBlock, fill bool) bool {
	tagname := msg.getHeader("Tag-Name")
	if tagname == "" {
		panic(throw("msgbox", "update to tag %s is malformed", t.tagname))
	}
	modified := false
	if t.tagname != tagname {
		if logEnable(logEMAILIN) {
			logit("in tag %s, Tag-Name is modified %q -> %q",
				msg.getHeader("Event-Number"), t.tagname, tagname)
		}
		t.tagname = tagname
		modified = true
	}
	if target := msg.getHeader("Target-Mark"); target != "" && target != t.committish {
		modified = true
		t.committish = target
	}
	if newtagger := msg.getHeader("Tagger"); newtagger != "" {
		newname, newemail, _, err := parseAttributionLine(newtagger)
		if err != nil || newname == "" || newemail == "" {
			panic(throw("msgbox", "Can't recognize address in Tagger: "+newtagger))
		} else if t.tagger.fullname != newname || t.tagger.email != newemail {
			t.tagger.fullname, t.tagger.email = newname, newemail
			if logEnable(logEMAILIN) {
				logit("in tag %s, Tagger is modified", msg.getHeader("Event-Number"))
			}
			modified = true
		}
		if taggerdate := msg.getHeader("Tagger-Date"); taggerdate != "" {
			date, err := newDate(taggerdate)
			if err != nil {
				panic(throw("msgbox", "malformed date %s in tag message: %v",
					taggerdate, err))
			}
			if !t.tagger.date.isZero() && !date.timestamp.Equal(t.tagger.date.timestamp) {
				// If self.repo is nil this is filling
				// in fields in a a new tag creation,
				// so suppress the usual message.
				if t.repo != nil {
					if logEnable(logSHOUT) {
						logit("in %s, Tagger-Date is modified '%v' -> '%v' (delta %v)",
							t.idMe(), t.tagger.date, taggerdate, date.timestamp.Sub(t.tagger.date.timestamp))
					}
				}
				modified = true
			}
			t.tagger.date = date
		}
	}

	if legacy := msg.getHeader("Legacy-ID"); legacy != "" && legacy != t.legacyID {
		modified = true
		t.legacyID = legacy
	}
	newcomment := msg.getPayload()
	if control.flagOptions["canonicalize"] {
		newcomment = canonicalizeComment(newcomment)
	}
	if newcomment != t.Comment {
		if logEnable(logEMAILIN) {
			logit("in tag %s, comment is modified %q -> %q",
				msg.getHeader("Event-Number"), t.Comment, newcomment)
		}
		modified = true
		t.Comment = newcomment
	}

	if fill && t.tagger.fullname == "" {
		t.tagger.fullname, t.tagger.email = whoami()
		modified = true
	}

	return modified
}

// decodable tells whether this tag is entirely composed of decodable UTF-8.
func (t *Tag) decodable() bool {
	valid := func(s string) bool {
		return utf8.Valid([]byte(s))
	}
	return valid(t.tagname) && valid(t.tagger.fullname) && valid(t.tagger.email) && valid(t.Comment)
}

// branchname returns the full branch reference corresponding to a tag.
func branchname(tagname string) string {
	fulltagname := tagname
	if strings.Count(tagname, "/") == 0 {
		fulltagname = "tags/" + fulltagname
	}
	if !strings.HasPrefix(fulltagname, "refs/") {
		fulltagname = "refs/" + fulltagname
	}
	return fulltagname
}

// stamp enables do_stamp() to report action stamps
func (t *Tag) stamp(_modifiers orderedStringSet, _eventnum int, cols int) string {
	firstLine, _ := splitRuneFirst(t.Comment, '\n')
	report := "<" + t.tagger.actionStamp() + "> " + firstLine
	if cols > 0 && len(report) > cols {
		report = report[0:cols]
	}
	return report
}

// Save this tag in import-stream format without constructing a string
func (t *Tag) Save(w io.Writer) {
	fmt.Fprintf(w, "tag %s\n", t.tagname)
	if t.legacyID != "" {
		fmt.Fprintf(w, "#legacy-id %s\n", t.legacyID)
	}
	fmt.Fprintf(w, "from %s\n", t.committish)
	if t.tagger != nil {
		fmt.Fprintf(w, "tagger %s\n", t.tagger)
	}
	comment := t.Comment
	if t.repo.writeOptions.Contains("--legacy") && t.legacyID != "" {
		if comment != "" {
			comment += control.lineSep
		}
		comment += fmt.Sprintf("Legacy-ID: %s\n", t.legacyID)
	}
	fmt.Fprintf(w, "data %d\n%s\n", len(comment), comment)
}

// String serializes this tag in import-stream format
func (t Tag) String() string {
	var bld strings.Builder
	t.Save(&bld)
	return bld.String()
}

func (t Tag) isCommit() bool {
	return false
}

// Reset represents a branch creation.
type Reset struct {
	ref        string
	committish string
	color      string
	legacyID   string // Sometines these are reduced Subversion commits
	repo       *Repository
	deleteme   bool
}

func newReset(repo *Repository, ref string, committish string, legacy string) *Reset {
	reset := new(Reset)
	reset.repo = repo
	reset.ref = ref
	reset.legacyID = legacy
	if committish != "" {
		reset.committish = committish
		reset.remember(repo, committish)
	}
	return reset
}

func (reset Reset) getDelFlag() bool {
	return reset.deleteme
}

func (reset *Reset) setDelFlag(b bool) {
	reset.deleteme = b
}

func (reset Reset) isCommit() bool {
	return false
}

// idMe IDs this reset for humans.
func (reset *Reset) idMe() string {
	return fmt.Sprintf("reset-%s@%d", reset.ref, reset.repo.eventToIndex(reset))
}

// getMark returns the reset's identifying mark
// Not actually used, needed to satisfy Event interface
func (reset Reset) getMark() string {
	return ""
}

// what to treat as a coment when message-boxing (dummy to satify Event)
func (reset Reset) getComment() string {
	return ""
}

// remember records an attachment to a repo and commit.
func (reset *Reset) remember(repo *Repository, committish string) {
	reset.repo = repo
	reset.committish = committish
	if event := repo.markToEvent(committish); event != nil {
		event.(*Commit).attach(reset)
	}
}

// forget loses this reset's attachment to its commit and repo.
func (reset *Reset) forget() {
	if event := reset.repo.markToEvent(reset.committish); event != nil {
		event.(*Commit).detach(reset)
	}
	reset.repo = nil
}

// moveto changes the repo this reset is associated with."
func (reset *Reset) moveto(repo *Repository) {
	reset.repo = repo
}

// tags enables do_tags() to report resets."
func (reset Reset) tags(modifiers orderedStringSet, eventnum int, _cols int) string {
	return fmt.Sprintf("%6d\treset\t%s", eventnum+1, reset.ref)
}

// Save this reset in import-stream format without constructing a string
func (reset *Reset) Save(w io.Writer) {
	if reset.repo.realized != nil {
		reset.repo.realized[reset.ref] = true
	}
	fmt.Fprintf(w, "reset %s\n", reset.ref)
	if reset.legacyID != "" {
		fmt.Fprintf(w, "#legacy-id %s\n", reset.legacyID)
	}
	if reset.committish != "" {
		fmt.Fprintf(w, "from %s\n\n", reset.committish)
		if reset.repo.branchPosition != nil {
			if p, ok := reset.repo.markToEvent(reset.committish).(*Commit); ok {
				reset.repo.branchPosition[reset.ref] = p
			}
		}
	} else {
		if reset.repo.branchPosition != nil {
			delete(reset.repo.branchPosition, reset.ref)
		}
	}
}

// String serializes this reset into a string
func (reset Reset) String() string {
	var bld strings.Builder
	reset.Save(&bld)
	return bld.String()
}

type optype byte

// FileOp is a gitspace file modification attached to a commit
type FileOp struct {
	repo       *Repository
	committish string
	Source     string
	mode       string
	Path       string
	ref        string
	inline     []byte
	op         optype
}

// Equals is an equality test for fileops
func (fileop *FileOp) Equals(other *FileOp) bool {
	return fileop.repo == other.repo &&
		fileop.committish == other.committish &&
		fileop.Source == other.Source &&
		fileop.mode == other.mode &&
		fileop.Path == other.Path &&
		fileop.ref == other.ref &&
		bytes.Equal(fileop.inline, other.inline) &&
		fileop.op == other.op
}

func newFileOp(repo *Repository) *FileOp {
	op := new(FileOp)
	op.repo = repo
	return op
}

func (fileop *FileOp) setOp(op optype) {
	fileop.op = op
}

// Thiis is a space-optimization hack.  We count on the compiler to
// put each of these in the text segment and pass around just one reference each.
// If we ever think the implementation has changed to falsify this assumption,
// we'll change these to var declarations and intern these strings explicitly.
const opM = 'M'
const opD = 'D'
const opR = 'R'
const opC = 'C'
const opN = 'N'
const opX = 'X' // used as a sentry value
const deleteall = 'd'

func (fileop *FileOp) construct(op optype, opargs ...string) *FileOp {
	fileop.op = op
	if op == 'M' {
		fileop.mode = opargs[0]
		fileop.ref = opargs[1]
		fileop.Path = opargs[2]
		if fileop.repo != nil && fileop.ref != "inline" {
			if blob, ok := fileop.repo.markToEvent(fileop.ref).(*Blob); ok {
				blob.appendOperation(fileop)
			}
		}
	} else if op == 'D' {
		fileop.Path = opargs[0]
	} else if op == 'N' {
		fileop.ref = opargs[0]
		fileop.Path = opargs[1]
	} else if op == 'R' {
		fileop.Source = opargs[0]
		fileop.Path = opargs[1]
	} else if op == 'C' {
		fileop.Source = opargs[0]
		fileop.Path = opargs[1]
	} else if op == 'd' {
	} else {
		panic(throw("parse", "unexpected fileop "+string(op)))
	}
	return fileop
}

// stringScan extracts tokens from a text line.  Tokens maky be
// "-quoted, in which the bounding quotes are stripped and C-style
// backslashes interpreted in the interior. Meant to mimic the
// behavior of git-fast-import.
func stringScan(input string, limit int) []string {
	bufs := make([][]byte, 0)
	state := 0
	tokenStart := func() {
		//fmt.Fprintf(os.Stderr, "New token\n")
		bufs = append(bufs, make([]byte, 0))
	}
	tokenContinue := func(c byte) {
		//fmt.Fprintf(os.Stderr, "%c: appending\n", c)
		bufs[len(bufs)-1] = append(bufs[len(bufs)-1], c)
	}
	toState := func(c byte, i int) int {
		//fmt.Fprintf(os.Stderr, "%c: %d -> %d\n", c, state, i)
		return i
	}
	for i := range input {
		c := input[i]
		//fmt.Fprintf(os.Stderr, "State %d, byte %c\n", state, c)
		switch state {
		case 0: // ground state, in whitespace
			if unicode.IsSpace(rune(c)) {
				continue
			} else if c == '"' {
				state = toState(c, 2)
				tokenStart()
				tokenContinue(c)
			} else {
				state = toState(c, 1)
				tokenStart()
				tokenContinue(c)
			}
		case 1: // in token
			if unicode.IsSpace(rune(c)) && len(bufs) < limit {
				state = toState(c, 0)
			} else {
				tokenContinue(c)
			}
		case 2: // in string
			if c == '"' {
				tokenContinue(c)
				state = toState(c, 0)
			} else if c == '\\' {
				state = toState(c, 3)
			} else {
				tokenContinue(c)
			}
		case 3: // after \ in string
			state = toState(c, 2)
			tokenContinue(c)
		}
	}

	out := make([]string, len(bufs))
	for i, tok := range bufs {
		s := string(tok)
		if s[0] == '"' {
			s, _ = strconv.Unquote(s)
		}
		out[i] = strings.TrimSpace(s)
	}
	return out
}

var modifyRE = regexp.MustCompile(`(M) ([0-9]+) (\S+) (.*)`)

// parse interprets a fileop dump line
func (fileop *FileOp) parse(opline string) *FileOp {
	if match, _ := regexp.MatchString(`^\s*$`, opline); match {
		panic(throw("parse", "Empty fileop line %q", opline))
	}
	if strings.HasPrefix(opline, "M ") {
		fields := stringScan(opline, 4)
		if len(fields) != 4 {
			panic(throw("parse", "Bad format of M line: %q", opline))
		}
		fileop.op = opM
		fileop.mode = string(fields[1])
		fileop.ref = string(fields[2])
		fileop.Path = string(fields[3])
	} else if strings.HasPrefix(opline, "N ") {
		fields := stringScan(opline, 3)
		if len(fields) != 3 {
			panic(throw("parse", "Bad format of N line: %q", opline))
		}
		fileop.op = opN
		fileop.ref = string(fields[1])
		fileop.Path = string(fields[2])
	} else if strings.HasPrefix(opline, "D ") {
		fields := stringScan(opline, 2)
		if len(fields) != 2 {
			panic(throw("parse", "Bad format of D line: %q", opline))
		}
		fileop.op = opD
		fileop.Path = string(fields[1])
	} else if strings.HasPrefix(opline, "R ") {
		fields := stringScan(opline, 3)
		if len(fields) != 3 {
			panic(throw("parse", "Bad format of R line: %q", opline))
		}
		fileop.op = opR
		fileop.Source = fields[1]
		fileop.Path = fields[2]
	} else if strings.HasPrefix(opline, "C ") {
		fields := stringScan(opline, 3)
		if len(fields) != 3 {
			panic(throw("parse", "Bad format of C line: %q", opline))
		}
		fileop.op = opC
		fileop.Source = fields[1]
		fileop.Path = fields[2]
	} else if strings.HasPrefix(opline, "deleteall") {
		fileop.op = deleteall
	} else {
		panic(throw("parse", "Unexpected fileop while parsing %q", opline))
	}
	return fileop
}

// forget removes the blob backreferences to a specified fileop.
func (fileop *FileOp) forget() {
	if fileop.repo == nil {
		return
	}
	if fileop.op == opM && fileop.ref != "inline" {
		if blob, ok := fileop.repo.markToEvent(fileop.ref).(*Blob); ok {
			blob.removeOperation(fileop)
		}
	}
}

// paths returns the set of all paths touched by this file op
func (fileop *FileOp) paths(pathtype orderedStringSet) orderedStringSet {
	if pathtype == nil {
		pathtype = orderedStringSet{string(opM), string(opD), string(opR), string(opC), string(opN)}
	}
	if !pathtype.Contains(string(fileop.op)) {
		return orderedStringSet{}
	}
	if fileop.op == opM || fileop.op == opD || fileop.op == opN {
		return orderedStringSet{fileop.Path}
	}
	if fileop.op == opR || fileop.op == opC {
		return orderedStringSet{fileop.Source, fileop.Path}
	}
	// Ugh...this isn't right for deleteall, but since we don't expect
	// to see that except at branch tips we'll ignore it for now.
	if fileop.op == deleteall {
		return orderedStringSet{}
	}
	panic("Unknown fileop type " + string(fileop.op))
}

// relevant tells if two fileops touch any of the same files
func (fileop *FileOp) relevant(other *FileOp) bool {
	if fileop.op == deleteall || other.op == deleteall {
		return true
	}
	return len(fileop.paths(nil).Intersection(other.paths(nil))) > 0
}

// Save dumps this fileop in import-stream format
func (fileop *FileOp) Save(w io.Writer) {
	quotifyIfNeeded := func(cpath string) string {
		if len(strings.Fields(cpath)) > 1 {
			return strconv.Quote(cpath)
		}
		return cpath
	}
	if fileop.op == opM {
		fmt.Fprintf(w, "M %s %s %s\n", fileop.mode, fileop.ref, quotifyIfNeeded(fileop.Path))
		if fileop.ref == "inline" {
			fmt.Fprintf(w, "data %d\n%s\n", len(fileop.inline), fileop.inline)
		}
		//return parts
	} else if fileop.op == opN {
		fmt.Fprintf(w, "N %s %s\n", fileop.ref, quotifyIfNeeded(fileop.Path))
		if fileop.ref == "inline" {
			fmt.Fprintf(w, "data %d\n%s\n", len(fileop.inline), fileop.inline)
		}
		//return parts
	} else if fileop.op == opD {
		fmt.Fprintf(w, "D %s\n", quotifyIfNeeded(fileop.Path))
	} else if fileop.op == opR || fileop.op == opC {
		fmt.Fprintf(w, "%c \"%s\" \"%s\"\n", fileop.op, fileop.Source, fileop.Path)
	} else if fileop.op == deleteall {
		w.Write([]byte("deleteall\n"))
	} else if fileop.op == 0 {
		// It's a nilOp, sometimes dumped during diagnostics
		w.Write([]byte("X\n"))
	} else {
		panic(throw("command", "Unexpected op %q while writing", fileop.op))
	}
}

// String serializes this FileOp in import-stream format
func (fileop FileOp) String() string {
	var bld strings.Builder
	fileop.Save(&bld)
	return bld.String()
}

// Copy returns a clone of the FileOp that calls it
func (fileop *FileOp) Copy() *FileOp {
	newop := newFileOp(fileop.repo)
	newop.committish = stringCopy(fileop.committish)
	newop.Source = stringCopy(fileop.Source)
	newop.mode = stringCopy(fileop.mode)
	newop.Path = stringCopy(fileop.Path)
	newop.ref = stringCopy(fileop.ref)
	newop.inline = make([]byte, len(fileop.inline))
	copy(newop.inline, fileop.inline)
	newop.op = fileop.op
	if newop.repo != nil && newop.ref != "inline" {
		if blob, ok := newop.repo.markToEvent(newop.ref).(*Blob); ok {
			blob.appendOperation(newop)
		}
	}
	return newop
}

// Callout is a stub object for callout marks in incomplete repository segments.
type Callout struct {
	mark        string
	branch      string
	_childNodes []string
	color       colorType
	deleteme    bool
}

func newCallout(mark string) *Callout {
	callout := new(Callout)
	callout.mark = mark
	callout._childNodes = make([]string, 0)
	return callout
}

func (callout *Callout) children() []CommitLike {
	var out []CommitLike
	return out
}
func (callout *Callout) hasChildren() bool {
	return false
}

func (callout Callout) getDelFlag() bool {
	return callout.deleteme
}

func (callout *Callout) setDelFlag(b bool) {
	callout.deleteme = b
}
func (callout Callout) callout() string {
	return callout.mark
}

func (callout Callout) getMark() string {
	return callout.mark
}

func (callout *Callout) idMe() string {
	return fmt.Sprintf("callout-%s", callout.mark)
}

// what to treat as a coment when message-boxing (dummy to satisfy Event)
func (callout Callout) getComment() string {
	return ""
}

// Save this blob in import-stream format without constructing a string
func (callout *Callout) Save(w io.Writer) {
	fmt.Fprintf(w, "callout-%s", callout.mark)
}

// String serializes this Callout in import-stream format
func (callout Callout) String() string {
	var bld strings.Builder
	callout.Save(&bld)
	return bld.String()
}

// moveto changes the repo this callout is associated with."
func (callout *Callout) moveto(*Repository) {
	// Has no repo field
}

func (callout Callout) isCommit() bool {
	return false
}

func (callout Callout) getColor() colorType {
	return callout.color
}

func (callout *Callout) setColor(color colorType) {
	callout.color = color
}

const (
	colorNONE            = 0
	colorEARLY colorType = 1 << iota // Errors and urgent messages
	colorLATE
	colorTRIVIAL
	colorDELETE
)

type colorType uint8
type colorSet uint8

func (c colorSet) Contains(a colorType) bool {
	return (c & colorSet(a)) != 0
}

func (c *colorSet) Add(a colorType) {
	*c |= colorSet(a)
}

func (c *colorSet) Remove(a colorType) {
	*c &= colorSet(^a)
}
func (c *colorSet) Clear() {
	*c = 0
}

// Manifest is a specialized PathMap containing FileOps
type Manifest struct {
	PathMap
}

func pmToManifest(pm *PathMap) *Manifest {
	return &Manifest{*pm}
}

func newManifest() *Manifest {
	return pmToManifest(newPathMap())
}

// Commit represents a commit event in a fast-export stream
type Commit struct {
	legacyID       string        // Commit's ID in an alien system
	mark           string        // Mark name of commit (may transiently be "")
	Comment        string        // Commit comment
	Branch         string        // branch name
	authors        []Attribution // Authors of commit
	committer      Attribution   // Person responsible for committing it.
	fileops        []*FileOp     // blob and file operation list
	_manifest      *Manifest     // efficient map of *Fileop values
	repo           *Repository
	properties     *OrderedMap  // commit properties (extension)
	attachments    []Event      // Tags and Resets pointing at this commit
	_parentNodes   []CommitLike // list of parent nodes
	_childNodes    []CommitLike // list of child nodes
	hash           gitHashType
	color          colorType // Scratch storage for graph-coloring
	deleteme       bool      // Flag used during deletion operations
	implicitParent bool      // Whether the first parent was implicit
}

func (commit Commit) getDelFlag() bool {
	return commit.deleteme
}

func (commit *Commit) setDelFlag(b bool) {
	commit.deleteme = b
}

func (commit Commit) getMark() string {
	return commit.mark
}

func newCommit(repo *Repository) *Commit {
	commit := new(Commit)
	commit.repo = repo
	commit.authors = make([]Attribution, 0)
	commit.fileops = make([]*FileOp, 0)
	commit.attachments = make([]Event, 0)
	commit._childNodes = make([]CommitLike, 0)
	commit._parentNodes = make([]CommitLike, 0)
	return commit
}

func (commit Commit) isCommit() bool {
	return true
}

func (commit Commit) getColor() colorType {
	return commit.color
}

func (commit *Commit) setColor(color colorType) {
	commit.color = color
}

func (commit *Commit) detach(event Event) bool {
	for i, el := range commit.attachments {
		if event == el {
			// Zero out the deleted element so it's GCed
			copy(commit.attachments[i:], commit.attachments[i+1:])
			commit.attachments[len(commit.attachments)-1] = nil
			commit.attachments = commit.attachments[:len(commit.attachments)-1]
			return true
		}
	}
	return false
}

func (commit *Commit) attach(event Event) {
	commit.attachments = append(commit.attachments, event)
}

// index gives the commit's 0-origin index in our repo.
func (commit *Commit) index() int {
	return commit.repo.eventToIndex(commit)
}

// getComment returns the comment attached to a commit
func (commit Commit) getComment() string { return commit.Comment }

// idMe IDs this commit for humans.
func (commit Commit) idMe() string {
	myid := fmt.Sprintf("commit@%s", commit.mark)
	if commit.legacyID != "" {
		myid += fmt.Sprintf("=<%s>", commit.legacyID)
	}
	return myid
}

// when returns an imputed timestamp for sorting after unites.
func (commit *Commit) when() time.Time {
	return commit.committer.date.timestamp
}

// date returns the commit date, for purpose of display and reference
func (commit *Commit) date() Date {
	if len(commit.authors) > 0 {
		return commit.authors[0].date
	}
	return commit.committer.date
}

// setBranch sets the repo's branch field.
func (commit *Commit) setBranch(branch string) {
	commit.Branch = branch
}

// operations returns a list of ileops associated with this commit;
// it hides how this is represented.
func (commit *Commit) operations() []*FileOp {
	return commit.fileops
}

// setOperations replaces the set of fileops associated with this commit.
func (commit *Commit) setOperations(ops []*FileOp) {
	commit.setOperationsNoInvalidate(ops)
	commit.invalidateManifests()
}

func (commit *Commit) setOperationsNoInvalidate(ops []*FileOp) {
	survives := make(map[*FileOp]bool)
	for _, op := range ops {
		survives[op] = true
	}
	for _, op := range commit.fileops {
		if !survives[op] {
			op.forget()
		}
	}
	commit.fileops = ops
	commit.hash.invalidate()
}

// appendOperation appends to the set of fileops associated with this commit.
func (commit *Commit) appendOperation(op *FileOp) {
	commit.fileops = append(commit.fileops, op)
	commit.invalidateManifests()
}

// prependOperation prepends to the set of fileops associated with this commit.
func (commit *Commit) prependOperation(op *FileOp) {
	commit.fileops = append([]*FileOp{op}, commit.fileops...)
	commit.invalidateManifests()
}

func (commit *Commit) prependOperations(ops []*FileOp) {
	newops := make([]*FileOp, 0, len(commit.operations())+len(ops))
	newops = append(newops, ops...)
	newops = append(newops, commit.operations()...)
	commit.fileops = newops
	commit.invalidateManifests()
}

// Since a deleteall clears all the content, no operation before it can impact
// the final result. The following helper discards all these file operations
// without changing the commit manifest.
func (commit *Commit) discardOpsBeforeLastDeleteAll() {
	for i := len(commit.fileops) - 1; i > 0; i-- {
		if commit.fileops[i].op == deleteall {
			// Remove backreferences from blobs to dropped operations
			for j := 0; j < i; j++ {
				commit.fileops[j].forget()
			}
			// Drop the fileops
			commit.fileops = commit.fileops[i:]
			break
		}
	}
}

// bump increments the timestamps on this commit to avoid time collisions.
func (commit *Commit) bump(i int) {
	delta := time.Second * time.Duration(i)
	commit.committer.date.timestamp = commit.committer.date.timestamp.Add(delta)
	for _, author := range commit.authors {
		author.date.timestamp = author.date.timestamp.Add(delta)
	}
	commit.hash.invalidate()
}

// clone replicates this commit, without its fileops, color, children, or tags.
func (commit *Commit) clone(repo *Repository) *Commit {
	var c = *commit // Was a Python deepcopy
	c.authors = make([]Attribution, len(commit.authors))
	copy(c.authors, commit.authors)
	// DO NOT USE setOperations because it would call forget
	// on each operation of the commit we are cloning from.
	c.fileops = nil
	//c.filemap = nil
	c.color = colorNONE
	if repo != nil {
		c.repo = repo
	}
	c._childNodes = nil
	// use the encapsulation to set parents instead of relying
	// on the copy, so that Commit can do its bookkeeping.
	c._parentNodes = nil // avoid confusing setParents()
	// Now that parents & children are correct, invalidate the manifest
	c.invalidateManifests()
	c.setParents(commit.parents())
	c.attachments = nil
	return &c
}

// showlegacy returns a legacy ID in the expected form for the ancestral system.
func (commit *Commit) showlegacy() string {
	if commit.legacyID == "" {
		return ""
	}
	// Special case for Subversion
	if commit.repo != nil && commit.repo.vcs != nil && commit.repo.vcs.name == "svn" {
		return "r" + commit.legacyID
	}
	return commit.legacyID
}

func (commit *Commit) hasProperties() bool {
	return commit.properties != nil
}

// lister enables DoList() to report commits.
func (commit *Commit) lister(_modifiers orderedStringSet, eventnum int, cols int) string {
	topline, _ := splitRuneFirst(commit.Comment, '\n')
	summary := fmt.Sprintf("%6d %s %6s %s ",
		eventnum+1, commit.date().rfc3339(), commit.mark, commit.gitHash().short())
	if commit.legacyID != "" {
		legacy := fmt.Sprintf("<%s>", commit.legacyID)
		summary += fmt.Sprintf("%6s ", legacy)
	}
	report := summary + topline
	if cols > 0 && len(report) > cols {
		report = report[:cols]
	}
	return report
}

// stamp enables DoStamp() to report action stamps.
func (commit *Commit) stamp(modifiers orderedStringSet, _eventnum int, cols int) string {
	firstLine, _ := splitRuneFirst(commit.Comment, '\n')
	report := "<" + commit.actionStamp() + "> " + firstLine
	if cols > 0 && len(report) > cols {
		report = report[:cols]
	}
	return report
}

// tags enables DoTags() to report tag tip commits.
func (commit *Commit) tags(_modifiers orderedStringSet, eventnum int, _cols int) string {
	if commit.Branch == "" || !strings.Contains(commit.Branch, "/tags/") {
		return ""
	}
	if commit.hasChildren() {
		successorBranches := newStringSet()
		for _, child := range commit.children() {
			switch child.(type) {
			case *Commit:
				successorBranches.Add(child.(*Commit).Branch)
			case *Callout:
				croak("internal error: callouts do not have branches: %s",
					child.idMe())
			default:
				panic("In tags method, unexpected type in child list")
			}
		}
		if successorBranches.Len() == 1 && successorBranches.Contains(commit.Branch) {
			return ""
		}
	}
	return fmt.Sprintf("%6d\tcommit\t%s", eventnum+1, commit.Branch)
}

// emailOut enables DoMsgout() to report commit metadata.
func (commit *Commit) emailOut(modifiers orderedStringSet,
	eventnum int, filterRegexp *regexp.Regexp) string {
	msg, _ := newMessageBlock(nil)
	msg.setHeader("Event-Number", fmt.Sprintf("%d", eventnum+1))
	msg.setHeader("Event-Mark", commit.mark)
	msg.setHeader("Branch", commit.Branch)
	msg.setHeader("Parents", strings.Join(commit.parentMarks(), " "))
	commit.committer.emailOut(modifiers, msg, "Committer")
	if len(commit.authors) > 0 {
		commit.authors[0].emailOut(modifiers, msg, "Author")
		for i, coauthor := range commit.authors[1:] {
			coauthor.emailOut(modifiers, msg, "Author"+fmt.Sprintf("%d", 2+i))
		}
	}
	if commit.legacyID != "" {
		msg.setHeader("Legacy-ID", commit.legacyID)
	}
	if commit.hasProperties() && len(commit.properties.keys) > 0 {
		for _, name := range commit.properties.keys {
			hdr := ""
			for _, s := range strings.Split(name, "-") {
				hdr += strings.Title(s)
			}
			value := commit.properties.get(name)
			value = strings.Replace(value, "\n", `\n`, -1)
			value = strings.Replace(value, "\r", `\r`, -1)
			value = strings.Replace(value, "\t", `\t`, -1)
			msg.setHeader("Property-"+hdr, value)
		}
	}
	check, _ := splitRuneFirst(commit.Comment, '\n')
	if len(check) > 54 {
		check = check[0:54]
	}
	msg.setHeader("Check-Text", check)
	msg.setPayload(commit.Comment)
	if commit.Comment != "" && !strings.HasSuffix(commit.Comment, "\n") && logEnable(logWARN) {
		logit("in commit %s, comment was not LF-terminated.", commit.mark)
	}

	if filterRegexp != nil {
		msg.filterHeaders(filterRegexp)
	}

	return msg.String()
}

// actionStamp controls how a commit stamp is made.
func (commit *Commit) actionStamp() string {
	// Prefer the author stamp because that doesn't change when patches
	// are replayed onto a repository, while the commit stamp will.
	if len(commit.authors) > 0 {
		return commit.authors[0].actionStamp()
	}
	return commit.committer.actionStamp()
}

func stringSliceEqual(a, b []string) bool {
	// If one is nil, the other must also be nil.
	if (a == nil) != (b == nil) {
		return false
	}

	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

var authorRE = regexp.MustCompile("Author[0-9]*$")

// emailIn updates this commit from a parsed email message.
func (commit *Commit) emailIn(msg *MessageBlock, fill bool) bool {
	modified := false
	newbranch := msg.getHeader("Branch")
	if newbranch != "" && commit.Branch != newbranch {
		modified = true
		commit.setBranch(newbranch)
	}
	newparents := msg.getHeader("Parents")
	if newparents != "" {
		newParentList := strings.Split(newparents, " ")
		if !stringSliceEqual(commit.parentMarks(), newParentList) {
			modified = true
			commit.setParentMarks(newParentList)
		}
	}
	c := &commit.committer
	newcommitter := msg.getHeader("Committer")
	if newcommitter != "" {
		var err2 error
		newfullname, newemail, _, err2 := parseAttributionLine(newcommitter)
		if err2 != nil {
			panic(throw("msgbox", "bad attribution: %v", err2))
		}
		if c.fullname != newfullname || c.email != newemail {
			c.fullname, c.email = newfullname, newemail
			if commit.repo != nil {
				if logEnable(logEMAILIN) {
					logit("in %s, Committer is modified", commit.idMe())
				}
			}
			modified = true
		}
	}
	rawdate := msg.getHeader("Committer-Date")
	if rawdate != "" {
		newcommitdate, err := newDate(rawdate)
		if err != nil {
			panic(throw("msgbox", "Bad Committer-Date: %#v (%v)", msg.getHeader("Committer-Date"), err))
		}
		if !c.date.isZero() && !newcommitdate.Equal(c.date) {
			if commit.repo != nil {
				if logEnable(logEMAILIN) {
					logit("in %s, Committer-Date is modified '%s' -> '%s' (delta %d)",
						commit.idMe(),
						c.date, newcommitdate,
						c.date.delta(newcommitdate))
				}
			}
			modified = true
		}
		c.date = newcommitdate
	}
	newauthor := msg.getHeader("Author")
	if newauthor != "" {
		authorkeys := []string{}
		for _, hd := range msg.hdnames {
			if len(authorRE.Find([]byte(hd))) > 0 {
				authorkeys = append(authorkeys, hd)
			}
		}
		// Potential minor bug here if > 10 authors;
		// lexicographic sort order doesn't match numeric
		// msg is *not* a dict so the .keys() is correct
		sort.Strings(authorkeys)
		for i := 0; i < len(authorkeys)-len(commit.authors); i++ {
			attrib, err := newAttribution(msg.getHeader(authorkeys[i]))
			if err != nil {
				panic(throw("msgbox", "bad author field: %v", err))
			}
			commit.authors = append(commit.authors, *attrib)
		}
		// Another potential minor bug: permuting the set of authors
		// will look like a modification, as old and new authors are
		// compaired pairwise rather than set equality being checked.
		// Possibly a feature if one thinks order is significant, but
		// I just did it this way because it was easier.
		for i, hdr := range authorkeys {
			c := &commit.authors[i]
			newfullname, newemail, _, err := parseAttributionLine(msg.getHeader(hdr))
			if err != nil {
				panic(throw("msgbox", "bad attribution: %v", err))
			}
			if c.fullname != newfullname || c.email != newemail {
				c.fullname, c.email = newfullname, newemail
				if logEnable(logEMAILIN) {
					logit("in commit %s, Author #%d is modified",
						msg.getHeader("Event-Number"), i+1)
				}
				modified = true
			}
			newdate := msg.getHeader(hdr + "-Date")
			if newdate != "" {
				date, err := newDate(newdate)
				if err != nil {
					panic(throw("msgbox", "Bad Author-Date: %v", err))
				}
				if !c.date.isZero() && !date.Equal(c.date) {
					eventnum := msg.getHeader("Event-Number")
					if commit.repo != nil && eventnum != "" {
						if logEnable(logEMAILIN) {
							logit("in event %s, %s-Date #%d is modified",
								eventnum, hdr, i+1)
						}
					}
					modified = true
				}
				c.date = date
			}
		}
	}
	newlegacy := msg.getHeader("Legacy-ID")
	if newlegacy != "" && newlegacy != commit.legacyID {
		modified = true
		commit.legacyID = newlegacy
	}
	newprops := newOrderedMap()
	for _, prophdr := range msg.hdnames {
		if !strings.HasPrefix(prophdr, "Property-") {
			continue
		}
		propkey := strings.ToLower(prophdr[9:])
		propval := msg.getHeader(prophdr)
		if propval == "True" || propval == "False" {
			newprops.set(propkey, propval)
		} else {
			newprops.set(propkey, strconv.Quote(propval))
		}
	}
	propsModified := (!commit.hasProperties() && newprops.Len() == 0) || !reflect.DeepEqual(newprops, commit.properties)
	if propsModified {
		commit.properties = &newprops
		modified = true
	}
	newcomment := msg.getPayload()
	if control.flagOptions["canonicalize"] {
		newcomment = canonicalizeComment(newcomment)
	}
	if newcomment != commit.Comment {
		if logEnable(logEMAILIN) {
			logit("in %s, comment is modified %q -> %q",
				commit.idMe(), commit.Comment, newcomment)
		}
		modified = true
		commit.Comment = newcomment
	}
	if fill {
		modified = true
		if commit.committer.date.isZero() {
			d, _ := newDate("")
			commit.committer.date = d
		}
		if commit.committer.fullname == "" {
			commit.committer.fullname, commit.committer.email = whoami()
		}
	}
	if modified {
		commit.hash.invalidate()
	}
	return modified
}

// setMark sets the commit's mark
func (commit *Commit) setMark(mark string) string {
	if commit.repo != nil {
		commit.repo.fixupMarkToIndex(commit, commit.mark, mark)
	}
	commit.mark = mark
	commit.hash.invalidate()
	return mark
}

// forget de-links this commit from its parents.
func (commit *Commit) forget() {
	commit.setParents([]CommitLike{})
	for _, fileop := range commit.operations() {
		if fileop.op == opN {
			commit.repo.inlines--
		}
	}
	commit.repo = nil
	commit.hash.invalidate()
}

// moveto changes the repo this commit is associated with.
func (commit *Commit) moveto(repo *Repository) {
	for _, fileop := range commit.operations() {
		fileop.repo = repo
		if fileop.op == opN {
			commit.repo.inlines--
			repo.inlines++
		}
	}
	commit.repo = repo
	commit.hash.invalidate()
}

// parents gets a list of this commit's parents.
func (commit *Commit) parents() []CommitLike {
	return commit._parentNodes
}

// invalidateManifests cleans out manifests in this commit and all descendants
func (commit *Commit) invalidateManifests() {
	// Do a traversal of the descendant graph, depth-first because it is the
	// most efficient with a slice as the queue.
	stack := []CommitLike{commit}
	for len(stack) > 0 {
		var current CommitLike
		// pop a CommitLike from the stack
		stack, current = stack[:len(stack)-1], stack[len(stack)-1]
		// remove the memoized manifest
		if c, ok := current.(*Commit); ok {
			if c._manifest == nil {
				// Because manifests are always generated recursively backwards
				// when one is requested and doesn't exist, if this commit's
				// manifest cache is nil none of its children can need clearing.
				continue
			}
			c._manifest = nil
		}
		// and add all children to the "todo" stack
		for _, child := range current.children() {
			stack = append(stack, child)
		}
	}
	commit.hash.invalidate()
}

// listMarks is only used for logging
func listMarks(items []CommitLike) []string {
	var out []string
	for _, x := range items {
		if x == nil {
			out = append(out, "nil")
		} else {
			out = append(out, x.getMark())
		}
	}
	return out
}

// parentMarks hides the parent list behind a wrapper, so that we
// can memoize the computation, which is very expensive and frequently
// performed.
func (commit *Commit) parentMarks() []string {
	var out []string
	for _, x := range commit._parentNodes {
		out = append(out, x.getMark())
	}
	return out
}

// commitRemove removes all instances of a commit/callout pointer from a list
func commitRemove(commitlist []CommitLike, commit CommitLike) []CommitLike {
	for i := len(commitlist) - 1; i >= 0; i-- {
		if commitlist[i] == commit {
			if i < len(commitlist)-1 {
				copy(commitlist[i:], commitlist[i+1:])
			}
			commitlist[len(commitlist)-1] = nil
			commitlist = commitlist[:len(commitlist)-1]
		}
	}
	return commitlist
}

func (commit *Commit) setParents(parents []CommitLike) {
	// remember the first parent
	var oldparent CommitLike
	if len(commit._parentNodes) > 0 {
		oldparent = commit._parentNodes[0]
	}
	for _, parent := range commit._parentNodes {
		// remove all occurrences of self in old parent's children cache
		switch parent.(type) {
		case *Commit:
			parent.(*Commit)._childNodes = commitRemove(parent.(*Commit)._childNodes, commit)
		case *Callout:
			croak("not removing callout %s", parent.(*Callout).mark)
		}
	}
	for _, c := range parents {
		if c == nil {
			panic("null commit in setParents()")
		}
	}
	commit._parentNodes = parents
	for _, parent := range commit._parentNodes {
		switch parent.(type) {
		case *Commit:
			parent.(*Commit)._childNodes = append(parent.(*Commit)._childNodes, commit)
		case *Callout:
			// do nothing
		}
	}
	// Only invalidate when needed: the manifest will not change if the first
	// parent is the same or the commit's first fileop is a deleteall cutting
	// ties with any first parent.
	if len(commit._parentNodes) == 0 || oldparent != commit._parentNodes[0] {
		if len(commit.fileops) == 0 || commit.fileops[0].op != deleteall {
			commit.invalidateManifests()
		}
	}
}

func (commit *Commit) setParentMarks(marks []string) {
	var clist []CommitLike
	for _, m := range marks {
		clist = append(clist, commit.repo.markToEvent(m).(CommitLike))
	}
	commit.setParents(clist)
}

func (commit *Commit) addParentCommit(newparent *Commit) {
	commit._parentNodes = append(commit._parentNodes, newparent)
	newparent._childNodes = append(newparent._childNodes, commit)
	// Only invalidate when needed: the manifest will not change if the first
	// parent is the same or the commit's first fileop is a deleteall cutting
	// ties with any first parent.
	if len(commit._parentNodes) == 1 { // there were no parents before
		if len(commit.fileops) == 0 || commit.fileops[0].op != deleteall {
			commit.invalidateManifests()
		}
	}
	commit.hash.invalidate()
}

func (commit *Commit) addParentByMark(mark string) {
	newparent := commit.repo.markToEvent(mark)
	if newparent == nil {
		panic(throw("parse", "Ill-formed stream: cannot resolve "+mark))
	}
	commit.addParentCommit(newparent.(*Commit))
}

// callout generates a callout cookie for this commit.
func (commit Commit) callout() string {
	result := commit.actionStamp()
	return result
}

// is_callot tells if the specified mark field a callout?"
func isCallout(mark string) bool {
	return strings.Contains(mark, "!")
}

func (commit *Commit) addCallout(mark string) {
	commit._parentNodes = append(commit._parentNodes, newCallout(mark))
}

func (commit *Commit) insertParent(idx int, mark string) {
	newparent := commit.repo.markToEvent(mark)
	if newparent == nil {
		croak("invalid mark %s passed to insertParent", mark)
		return
	}
	// Stupid slice tricks: https://github.com/golang/go/wiki/SliceTricks
	commit._parentNodes = append(commit._parentNodes[:idx], append([]CommitLike{newparent.(*Commit)}, commit._parentNodes[idx:]...)...)
	switch newparent.(type) {
	case *Commit:
		newparent.(*Commit)._childNodes = append(newparent.(*Commit)._childNodes, commit)
	}
	commit.invalidateManifests()
}

func (commit *Commit) removeParent(event CommitLike) {
	// remove *all* occurences of event in parents
	commit._parentNodes = commitRemove(commit._parentNodes, event)
	// and all occurences of self in event's children
	if commit, ok := event.(*Commit); ok {
		commit._childNodes = commitRemove(commit._childNodes, commit)
		commit.invalidateManifests()
	}
	commit.hash.invalidate()
}

func (commit *Commit) replaceParent(e1, e2 *Commit) {
	if e2 == nil {
		panic("null commit in replaceParents()")
	}
	for i, item := range commit._parentNodes {
		if item == e1 {
			commit._parentNodes[i] = e2
			e1._childNodes = commitRemove(e1._childNodes, commit)
			e2._childNodes = append(e2._childNodes, commit)
			commit.invalidateManifests()
			return
		}
	}
	commit.invalidateManifests()
}

func (commit *Commit) hasParents() bool {
	return len(commit._parentNodes) > 0
}

func (commit *Commit) hasCallouts() bool {
	for _, c := range commit._parentNodes {
		switch c.(type) {
		case *Callout:
			return true
		}
	}

	return false
}

// children gets a list of this commit's children (Commits or Callouts)."
func (commit *Commit) children() []CommitLike {
	return commit._childNodes
}

func (commit *Commit) childMarks() []string {
	var out []string
	for _, x := range commit._childNodes {
		out = append(out, x.getMark())
	}
	return out
}

// hasChildren is a predicate - does this commit have children?"
func (commit *Commit) hasChildren() bool {
	return len(commit._childNodes) > 0
}

// firstChild gets the first child of this commit, or None if not hasChildren()."
func (commit *Commit) firstChild() *Commit {
	if len(commit._childNodes) == 0 {
		return nil
	}
	return commit._childNodes[0].(*Commit)
}

// descendedFrom tells if this commit a descendant of the specified other?
func (commit *Commit) descendedFrom(other *Commit) bool {
	if !commit.hasParents() {
		return false
	}
	for _, item := range commit.parents() {
		if item == other {
			return true
		}
	}
	for _, item := range commit.parents() {
		switch item.(type) {
		case *Commit:
			if item.(*Commit).descendedFrom(other) {
				return true
			}
		}
	}

	return false
}

// fileopDump reports file ops without data or inlines; used for logging only.
func (commit *Commit) fileopDump(output io.Writer) {
	fmt.Fprintf(output, "commit %d, mark %s:\n", commit.repo.markToIndex(commit.mark)+1, commit.mark)
	for i, op := range commit.operations() {
		fmt.Fprintf(output, "%d: %s", i, op.String())
	}
}

// paths returns the set of all paths touched by this commit.
func (commit *Commit) paths(pathtype orderedStringSet) orderedStringSet {
	pathset := make([]string, 0)
	seen := make(map[string]bool, len(commit.operations()))
	for _, fileop := range commit.operations() {
		for _, item := range fileop.paths(pathtype) {
			if !seen[item] {
				seen[item] = true
				pathset = append(pathset, item)
			}
		}
	}
	return orderedStringSet(pathset)
}

// visible tells if a path is modified and not deleted in the ancestors
func (commit *Commit) visible(argpath string) *Commit {
	ancestor := commit
	for {
		parents := ancestor.parents()
		if len(parents) == 0 {
			return nil
		}
		switch parents[0].(type) {
		case *Callout:
			return nil
		case *Commit:
			ancestor = parents[0].(*Commit)
			// This loop assumes that the op sequence has no
			// M/C/R foo after D foo pairs. If this condition
			// is violated it can throw false negatives.
			for _, fileop := range ancestor.operations() {
				if fileop.Path == argpath {
					if fileop.op == opD {
						return nil
					}
					return ancestor
				}
			}
		}
	}
	// unreachable
}

// manifest returns a map from all pathnames visible at this commit
// to Fileop structures. The map contents is shared as much as
// possible with manifests from previous commits to keep working-set
// size to a minimum.
func (commit *Commit) manifest() *Manifest {
	// yeah, baby this operation is *so* memoized...
	if commit._manifest != nil {
		return commit._manifest
	}
	// Git only inherits files from the first parent of a commit.
	// The simplest idea is to ask the first parent to compute its manifest,
	// which will ask its own first parent recursively back to the root commit,
	// or thanks to memoization, a commit whose manifest is already known.
	// Since this might exceed the recursion depth limit, a more robust way is
	// to walk the chain of "first parents" until a commit whose manifest is
	// known, remembering which commits need to have their manifest computed.
	commitsToHandle := []*Commit{}
	ancestor := commit
	for ancestor._manifest == nil {
		commitsToHandle = append(commitsToHandle, ancestor)
		if !ancestor.hasParents() {
			break
		}
		switch p := ancestor.parents()[0].(type) {
		case *Commit:
			ancestor = p
		case *Callout:
			croak("internal error: can't get through a callout")
			break
		default:
			panic("manifest() found unexpected type in parent list")
		}
	}
	// commitsToHandle now contains all the commits whose manifest need to be
	// computed to be able to compute the one initially asked for.
	// By construction, each commit in that list is the child of the next, and
	// if ancestor._manifest is not nil, then ancestor is the parent of the
	// last commit in commitsToHandle, and ancestor._manifest should be
	// inherited by that last commit.
	// Else, ancestor._manifest is nil and the loop was broken because the last
	// commit in commitsToHandle has no parent or a non-commit first parent. In
	// that case the manifest inherited by the last commit is just empty.
	manifest := ancestor._manifest
	if manifest == nil {
		manifest = newManifest()
	}
	// Now loop over commitsToHandle, starting from the end. At the start of each iteration,
	// manifest contains the manifest inherited from the first parent, if any.
	for k := len(commitsToHandle) - 1; k >= 0; k-- {
		// Take own fileops into account.
		commit := commitsToHandle[k]
		pm := manifest.snapshot()
		commit.applyFileOps(pm, false, false)
		manifest = pmToManifest(pm)
		commit._manifest = manifest
	}
	return manifest
}

// Walk along the repository commits, computing and forgetting manifests as we
// go. The manifest of the commit and its first parent are guaranteed to be
// memoized, but any other might have been forgotten to minimize the working set
func (repo *Repository) walkManifests(
	hook func(idx int, commit *Commit, fistParentIdx int, firstParent *Commit)) {
	childrenToHandle := make(map[int]int)
	for index, event := range repo.events {
		if commit, ok := event.(*Commit); ok {
			inheritingChildren := 0
			for _, child := range commit.children() {
				if childcommit, ok := child.(*Commit); ok &&
					childcommit.parents()[0] == commit {
					inheritingChildren++
				}
			}
			if commit._manifest != nil {
				inheritingChildren = -1 // Mark as already memoized
			}
			firstParentIdx := -1
			var firstParent *Commit
			if commit.hasParents() {
				if parent, ok := commit.parents()[0].(*Commit); ok {
					firstParent = parent
					firstParentIdx = repo.eventToIndex(parent)
				}
			}
			commit.manifest() // Compute and memoize
			hook(index, commit, firstParentIdx, firstParent)
			if inheritingChildren == 0 {
				// Forget the manifest right away as commit has no children
				// inheriting from it.
				commit._manifest = nil
			} else {
				// Remember the number of children so that we can forget
				// the manifest at the correct time.
				childrenToHandle[index] = inheritingChildren
			}
			if firstParentIdx == -1 {
				continue
			}
			childrenToHandle[firstParentIdx]--
			if childrenToHandle[firstParentIdx] == 0 {
				delete(childrenToHandle, firstParentIdx)
				firstParent._manifest = nil // Forget the now unneeded manifest
			}
		}
	}
	// Cleanup remaining manifests
	for index, val := range childrenToHandle {
		if val >= 0 {
			repo.events[index].(*Commit)._manifest = nil
		}
	}
}

// The object formats we're mimicking for hashing purposes are described here:
// https://www.git-scm.com/book/en/v2/Git-Internals-Git-Objects
// https://stackoverflow.com/questions/14790681/what-is-the-internal-format-of-a-git-tree-object

func (manifest *Manifest) gitHash() gitHashType {
	type Element struct {
		name string
		mode string
		hash gitHashType
	}
	var innerHash func(pm *PathMap) gitHashType
	innerHash = func(pm *PathMap) gitHashType {
		if hash, ok := pm.info.(gitHashType); ok {
			return hash
		}
		elements := []Element{}
		for name, subdir := range pm.dirs {
			elements = append(elements, Element{
				mode: "40000",
				name: name,
				hash: innerHash(subdir),
			})
		}
		for name, entry := range pm.blobs {
			op := entry.(*FileOp)
			if blob, ok := op.repo.markToEvent(op.ref).(*Blob); ok {
				elements = append(elements, Element{
					mode: op.mode,
					name: name,
					hash: blob.gitHash(),
				})
			} else {
				// The ref is not a blob mark. This is probably a git link,
				// or a hash given directly.
				hashref, _ := hex.DecodeString(op.ref)
				hash := gitHashType{}
				copy(hash[:], hashref)
				elements = append(elements, Element{
					mode: op.mode,
					name: name,
					hash: hash,
				})
			}
		}
		sort.Slice(elements, func(i, j int) bool {
			return elements[i].name < elements[j].name
		})
		var sb strings.Builder
		for _, e := range elements {
			fmt.Fprintf(&sb, "%s %s\x00%s", e.mode, e.name, e.hash)
		}
		body := sb.String()
		hash := gitHashString(fmt.Sprintf("tree %d\x00%s", len(body), body))
		if pm.shared { // The PathMap is immutable, we can cache its hash
			pm.info = hash
		}
		return hash
	}
	return innerHash(&manifest.PathMap)
}

func (commit *Commit) gitHash() gitHashType {
	if !commit.hash.isValid() {
		var sb strings.Builder
		// Assumptin: Git running under DOS still uses plain \n as a
		// line separator. If this isn't true these "\n"s need to be
		// replaced by control.lineSep.
		sb.WriteString("tree " + commit.manifest().gitHash().hexify() + "\n")
		for _, parent := range commit.parents() {
			switch parent.(type) {
			case *Commit:
				sb.WriteString("parent " + parent.(*Commit).gitHash().hexify() + "\n")
			case *Callout:
				// Ignore this case
			default:
				panic("In gitHash method, unexpected type in child list")
			}
		}
		// Git doesn't support multiple authors, so we'll probably see
		// bogons if there's ever more than one generated in here.
		// But this loop is uniform
		for _, author := range commit.authors {
			sb.WriteString("author " + author.String() + "\n")
		}
		sb.WriteString("committer " + commit.committer.String() + "\n")
		sb.WriteString("\n")
		sb.WriteString(commit.Comment)
		body := sb.String()
		commit.hash = gitHashString(fmt.Sprintf("commit %d\x00", len(body)) + body)
	}
	return commit.hash
}

// canonicalize replaces fileops by a minimal set of D and M with same result.
func (commit *Commit) canonicalize() {
	// Discard everything before the last deleteall
	commit.discardOpsBeforeLastDeleteAll()
	ops := commit.operations()
	if len(ops) == 0 {
		return
	}
	// Fetch the tree state before us...
	var previous PathMapLike
	if !commit.hasParents() {
		previous = &FlatPathMap{}
	} else {
		p := commit.parents()[0]
		switch p.(type) {
		case *Commit:
			previous = p.(*Commit).manifest()
		case *Callout:
			croak("internal error: can't get through a callout")
			return
		default:
			panic("manifest() found unexpected type in parent list")
		}
	}
	// And our tree state
	current := commit.manifest()
	newops := newOrderedFlatPathMap()
	// Generate needed D fileops.
	if commit.fileops[0].op == deleteall {
		// There was a deleteall, all paths in previous can disappear
		previous.iter(func(cpath string, _ interface{}) {
			if _, found := current.get(cpath); !found {
				fileop := newFileOp(commit.repo)
				fileop.construct(opD, cpath)
				newops.set(cpath, fileop)
			}
		})
	} else {
		// There was no deleteall, only sources of R operations or
		// files with a D operation might disappear.
		for _, fileop := range ops {
			cpath := ""
			if fileop.op == opR {
				cpath = fileop.Source
			} else if fileop.op == opD {
				cpath = fileop.Path
			} else {
				continue
			}
			_, old := previous.get(cpath)
			_, new := current.get(cpath)
			if old && !new {
				fileop := newFileOp(commit.repo)
				fileop.construct(opD, cpath)
				newops.set(cpath, fileop)
			}
		}
	}
	// Generate needed M fileops. Only targets of R, C and M ops
	// can be changed.
	for _, fileop := range ops {
		if fileop.op == opD {
			continue
		}
		cpath := fileop.Path
		ioe, oldok := previous.get(cpath)
		ine, newok := current.get(cpath)
		oe, _ := ioe.(*FileOp)
		ne, _ := ine.(*FileOp)
		if newok && !(oldok && oe.Equals(ne)) {
			newops.set(cpath, ne.Copy())
		}
	}
	// Now replace the Commit fileops, not passing through any deleteall
	commit.remakeFileOps(newops, false)
	commit.hash.invalidate()
}

// alldeletes is a predicate: is this an all-deletes commit?
func (commit *Commit) alldeletes(killset ...optype) bool {
	if killset == nil {
		killset = []optype{opD, deleteall}
	}
	for _, fileop := range commit.operations() {
		match := false
		for _, op := range killset {
			if fileop.op == op {
				match = true
				break
			}
		}
		if !match {
			return false
		}
	}

	return true
}

// checkout makes a directory with links to files in a specified checkout.
func (commit *Commit) checkout(directory string) string {
	if directory == "" {
		directory = filepath.FromSlash(commit.repo.subdir("") + "/" + commit.mark)
	}
	if !exists(directory) {
		commit.repo.makedir("checkout")
		os.Mkdir(directory, userReadWriteSearchMode)
	}

	defer func() {
		if r := recover(); r != nil {
			croak("could not create checkout directory or files: %v.", r)
		}
	}()

	commit.manifest().iter(func(cpath string, pentry interface{}) {
		entry := pentry.(*FileOp)
		fullpath := filepath.FromSlash(directory +
			"/" + cpath + "/" + entry.ref)
		if !exists(fullpath) {
			parts := strings.Split(fullpath, "/")
			// os.MkdirAll is broken and rpike says they
			// won't fix it.
			// https://github.com/golang/go/issues/22323
			var dpath string
			for i := range parts[0 : len(parts)-1] {
				dpath = filepath.FromSlash(strings.Join(parts[:i+1], "/"))
				err := os.Mkdir(dpath, userReadWriteSearchMode)
				if err != nil && !os.IsExist(err) {
					panic(fmt.Errorf("Directory creation failed during checkout: %v", err))
				}

			}
			rawmode, err2 := strconv.ParseUint(entry.mode, 8, 32)
			if err2 != nil {
				panic(err2)
			}
			mode := os.FileMode(rawmode)
			blob := commit.repo.markToEvent(entry.ref).(*Blob)
			if entry.ref == "inline" {
				file, err3 := os.OpenFile(blob.getBlobfile(true),
					os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
				if err3 != nil {
					panic(fmt.Errorf("File creation for inline failed during checkout: %v", err3))
				}
				file.Write([]byte(entry.inline))
				blob.size = int64(len(entry.inline))
				file.Close()
			} else {
				if blob.hasfile() {
					os.Link(blob.getBlobfile(false), fullpath)
				} else {
					file, err4 := os.OpenFile(blob.getBlobfile(true),
						os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
					if err4 != nil {
						panic(fmt.Errorf("File creation failed during checkout: %v", err4))
					}
					content := blob.getContent()
					file.Write(content)
					blob.size = int64(len(content))
					file.Close()
				}
			}
		}
	})
	return directory
}

// head returns the branch to which this commit belongs.
func (commit *Commit) head() string {
	if strings.HasPrefix(commit.Branch, "refs/heads/") || !commit.hasChildren() {
		return commit.Branch
	}
	rank := 0
	var child Event
	for rank, child = range commit.children() {
		switch child.(type) {
		case *Commit:
			if child.(*Commit).Branch == commit.Branch {
				return child.(*Commit).head()
			}
		case *Callout:
			croak("internal error: callouts do not have branches: %s",
				child.idMe())
		}
	}
	if rank == 0 {
		switch child.(type) {
		case *Commit:
			return child.(*Commit).head() // there was only one child
		case *Callout:
			croak("internal error: callouts do not have branches: %s",
				child.idMe())
		}
	}
	croak("Can't deduce a branch head for %s", commit.mark)
	return ""
}

// tip enables do_tip() to report deduced branch tips.
func (commit *Commit) tip(_modifiers orderedStringSet, eventnum int, cols int) string {
	summary := fmt.Sprintf("%6d %s %6s ",
		eventnum+1, commit.date().rfc3339(), commit.mark)
	report := summary + commit.head()
	if cols > 0 && len(report) > cols {
		report = report[:cols]
	}
	return report
}

// reference answers whether this commit references a specified blob mark.
func (commit *Commit) references(mark string) bool {
	for _, fileop := range commit.operations() {
		if fileop.op == opM && fileop.ref == mark {
			return true
		}
	}
	return false
}

// blobByName looks up file content by name
func (commit *Commit) blobByName(pathname string) ([]byte, bool) {
	value, ok := commit.manifest().get(pathname)
	if !ok {
		return []byte{}, false
	}
	entry := value.(*FileOp)
	if entry.ref == "inline" {
		return entry.inline, true
	}
	retrieved := commit.repo.markToEvent(entry.ref)
	switch retrieved.(type) {
	case *Blob:
		return retrieved.(*Blob).getContent(), true
	default:
		errmsg := fmt.Sprintf("Unexpected type while attempting to fetch %s content at %s", pathname, entry.ref)
		panic(errmsg)
	}
}

// decodable tells whether this commit is entirely composed of decodable UTF-8.
func (commit *Commit) decodable() bool {
	valid := func(s string) bool {
		return utf8.Valid([]byte(s))
	}
	if !(valid(commit.committer.fullname) && valid(commit.committer.email) && valid(commit.Comment)) {
		return false
	}
	for _, author := range commit.authors {
		if !valid(author.fullname) || !valid(author.email) {
			return false
		}
	}
	return true
}

// delete severs this commit from its repository.
func (commit *Commit) delete(policy orderedStringSet, baton *Baton) {
	commit.repo.delete([]int{commit.index()}, policy, baton)
}

// Save this commit to a stream in fast-import format
func (commit *Commit) Save(w io.Writer) {
	vcs := commit.repo.preferred
	if vcs == nil && commit.repo.vcs != nil && commit.repo.vcs.importer != "" {
		vcs = commit.repo.vcs
	}
	incremental := false
	if !commit.repo.writeOptions.Contains("--noincremental") {
		if commit.repo.realized != nil && commit.hasParents() {
			if _, ok := commit.repo.realized[commit.Branch]; !ok {
				parent := commit.parents()[0]
				switch parent.(type) {
				case *Commit:
					pbranch := parent.(*Commit).Branch
					if !commit.repo.realized[pbranch] {
						incremental = true
					}
				}
			}
		}
	}
	// getting a value from a nil map is safe
	previousOnBranch := commit.repo.branchPosition[commit.Branch]
	if incremental {
		fmt.Fprintf(w, "reset %s\nfrom %s^0\n\n", commit.Branch, commit.Branch)
	} else if len(commit.parents()) == 0 && previousOnBranch != nil {
		fmt.Fprintf(w, "reset %s\n", commit.Branch)
	}
	if commit.repo.branchPosition != nil {
		commit.repo.branchPosition[commit.Branch] = commit
	}
	fmt.Fprintf(w, "commit %s\n", commit.Branch)
	if commit.legacyID != "" {
		fmt.Fprintf(w, "#legacy-id %s\n", commit.legacyID)
	}
	if commit.repo.realized != nil {
		commit.repo.realized[commit.Branch] = true
	}
	if commit.mark != "" {
		fmt.Fprintf(w, "mark %s\n", commit.mark)
	}
	if commit.hash.isValid() {
		fmt.Fprintf(w, "original-oid %s\n", commit.hash.hexify())
	}
	if len(commit.authors) > 0 {
		for _, author := range commit.authors {
			fmt.Fprintf(w, "author %s\n", author)
		}
	}
	if commit.committer.fullname != "" {
		fmt.Fprintf(w, "committer %s\n", commit.committer)
	}
	// As of git 2.13.6 (possibly earlier) the comment field of
	// commit is no longer optional - you have to emit data 0 if there
	// is no comment, otherwise the importer gets confused.
	comment := commit.Comment
	if commit.repo.writeOptions.Contains("--legacy") && commit.legacyID != "" {
		if comment != "" {
			comment += control.lineSep
		}
		comment += fmt.Sprintf("Legacy-ID: %s\n", commit.legacyID)
	}
	fmt.Fprintf(w, "data %d\n%s", len(comment), comment)
	if len(comment) > 0 && comment[len(comment)-1] != '\n' {
		w.Write([]byte{'\n'})
	}
	if commit.repo.exportStyle().Contains("nl-after-comment") {
		w.Write([]byte{'\n'})
	}
	parents := commit.parents()
	doCallouts := commit.repo.writeOptions.Contains("--callout")
	noImplicit := commit.repo.writeOptions.Contains("--no-implicit")
	if len(parents) > 0 {
		ancestor := parents[0]
		if (commit.repo.internals == nil && !incremental) || commit.repo.internals.Contains(ancestor.getMark()) {
			if noImplicit || !(commit.implicitParent &&
				previousOnBranch == ancestor && len(parents) == 1) {
				fmt.Fprintf(w, "from %s\n", ancestor.getMark())
			}
		} else if doCallouts {
			fmt.Fprintf(w, "from %s\n", ancestor.callout())
		}
		for _, ancestor := range parents[1:] {
			var nugget string
			if commit.repo.internals == nil || commit.repo.internals.Contains(ancestor.getMark()) {
				nugget = ancestor.getMark()
			} else if doCallouts {
				nugget = ancestor.callout()
			}
			if nugget != "" {
				fmt.Fprintf(w, "merge %s\n", nugget)
			}
		}
	}
	if vcs != nil && vcs.extensions.Contains("commit-properties") {
		if commit.hasProperties() && len(commit.properties.keys) > 0 {
			for _, name := range commit.properties.keys {
				value := commit.properties.get(name)
				if value == "true" || value == "false" {
					if value != "" {
						fmt.Fprintf(w, "property %s\n", name)
					}
				} else {
					fmt.Fprintf(w, "property %s %d %s\n", name, len(value), value)
				}
			}
		}
	}
	for _, op := range commit.operations() {
		w.Write([]byte(op.String()))
	}
	if !commit.repo.exportStyle().Contains("no-nl-after-commit") {
		w.Write([]byte{'\n'})
	}
}

// String serializes this commit in import-stream format
func (commit Commit) String() string {
	var bld strings.Builder
	commit.Save(&bld)
	return bld.String()
}

func (commit Commit) commonDirectory() string {
	// Adapted from https://github.com/jpillora/longestcommon/blob/master/lc.go
	// Short-circuit empty list
	if len(commit.fileops) == 0 {
		return ""
	}
	prefix := path.Dir(commit.fileops[0].Path) + "/"
	// Short-circuit single-element list
	if len(commit.fileops) == 1 {
		return prefix
	}
	// Compare first to rest
	for _, fileop := range commit.fileops[1:] {
		prefixl := len(prefix)
		strl := len(fileop.Path)
		// Short-circuit empty strings
		if prefixl == 0 || strl == 0 {
			return ""
		}
		// Maximum possible length
		maxl := prefixl
		if strl < maxl {
			maxl = strl
		}
		// Prefix, iterate left to right
		for i := 0; i < maxl; i++ {
			if prefix[i] != fileop.Path[i] {
				prefix = prefix[:i]
				break
			}
		}
	}
	if strings.Contains(prefix, "/") {
		prefix = prefix[:strings.LastIndex(prefix, "/")+1]
	}
	return prefix
}

//Passthrough represents a passthrough line.
type Passthrough struct {
	repo     *Repository
	text     string
	color    string
	deleteme bool
}

func (p Passthrough) getDelFlag() bool {
	return p.deleteme
}

func (p *Passthrough) setDelFlag(b bool) {
	p.deleteme = b
}

func newPassthrough(repo *Repository, line string) *Passthrough {
	p := new(Passthrough)
	p.repo = repo
	p.text = line
	// Don't do this!  These sometimes need to be added to the front.
	//if repo != nil {
	//	repo.events = append(repo.events, p)
	//}
	return p
}

// emailOut enables DoMsgout() to report these.
func (p *Passthrough) emailOut(_modifiers orderedStringSet,
	eventnum int, _filterRegexp *regexp.Regexp) string {
	msg, _ := newMessageBlock(nil)
	msg.setHeader("Event-Number", fmt.Sprintf("%d", eventnum+1))
	msg.setPayload(p.text)
	return msg.String()
}

func (p *Passthrough) emailIn(msg *MessageBlock) {
	p.text = msg.getPayload()
}

// idMe IDs this passthrough for humans."
func (p *Passthrough) idMe() string {
	return fmt.Sprintf("passthrough@%d", p.repo.eventToIndex(p))
}

//getMark is a stub required for the Event interface
func (p Passthrough) getMark() string {
	return ""
}

// getComment returns the text attached to a passthrough
func (p Passthrough) getComment() string { return p.text }

// String reports this passthrough in import-stream format.
func (p Passthrough) String() string {
	return p.text
}

// Save reports this passthrough in import-stream format.
func (p *Passthrough) Save(w io.Writer) {
	w.Write([]byte(p.text))
}

// moveto changes the repo this passthrough is associated with."
func (p *Passthrough) moveto(*Repository) {
	// Has no repo field
}

func (p Passthrough) isCommit() bool {
	return false
}

// Generic extractor code begins here

// capture runs a specified command, capturing the output.
func captureFromProcess(command string, baton *Baton) (string, error) {
	if logEnable(logCOMMANDS) {
		logit("%s: capturing %s", rfc3339(time.Now()), command)
	}
	cmd := exec.Command("sh", "-c", command)
	content, err := cmd.CombinedOutput()
	if logEnable(logCOMMANDS) {
		baton.printLog(content)
	}
	return string(content), err
}

// branchbase returns the branch minus refs/heads or refs/tags.
func branchbase(branch string) string {
	for _, p := range []string{"refs/heads/", "refs/tags/"} {
		if strings.HasPrefix(branch, p) {
			return branch[len(p):]
		}
	}
	return filepath.Base(branch)
}

// StreamParser parses a fast-import stream or Subversion dump to
// populate a Repository.
type StreamParser struct {
	repo        *Repository
	fp          *bufio.Reader // Can't be os.File, unit tests will fail
	source      string
	importLine  int
	ccount      int64
	linebuffers [][]byte
	lastcookie  Cookie
	svnReader   // Opaque state of the Subversion dump reader
}

// newSteamParser parses a fast-import stream or Subversion dump to a Repository.
func newStreamParser(repo *Repository) *StreamParser {
	sp := new(StreamParser)
	sp.repo = repo
	sp.linebuffers = make([][]byte, 0)
	return sp
}

func (sp *StreamParser) error(msg string) {
	// Throw fatal error during parsing.
	panic(throw("parse", "%d: %s", sp.importLine, msg))
}

func (sp *StreamParser) errorLocation() string {
	// Alas, must use old format here because of the leading log tag
	if sp.importLine > 0 {
		leader := ""
		if sp.source != "" {
			leader = fmt.Sprintf(`"%s", `, sp.source)
		}
		return fmt.Sprintf(leader+"line %d: ", sp.importLine)
	}
	return ""
}

func (sp *StreamParser) warn(msg string) {
	// Display a parse warning associated with a line but don't error out.
	if logEnable(logWARN) {
		logit(sp.errorLocation() + msg)
	}
}

func (sp *StreamParser) shout(msg string) {
	// A gripe with line number
	if logEnable(logSHOUT) {
		logit(sp.errorLocation() + msg)
	}

}

func (sp *StreamParser) read(n int) []byte {
	// Read possibly binary data
	buf := make([]byte, n)
	_, err := io.ReadFull(sp.fp, buf)
	if err != nil {
		sp.error("bad read in data")
	}
	sp.ccount += int64(n)
	sp.importLine += bytes.Count(buf, []byte("\n"))
	return buf
}

func (sp *StreamParser) readline() []byte {
	// Read a newline-terminated string, returning "" at EOF
	var line []byte
	if len(sp.linebuffers) > 0 {
		line = []byte(sp.linebuffers[0])
		sp.linebuffers = sp.linebuffers[1:]
	} else {
		var err error
		line, err = sp.fp.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				line = []byte{}
			} else {
				panic(throw("parse", "in readline(): %v", err))
			}
		}
	}
	sp.ccount += int64(len(line))
	sp.importLine++
	return line
}

func (sp *StreamParser) tell() int64 {
	// Return the current read offset in the source stream.
	if sp.fp == nil {
		return noOffset
	}
	return sp.ccount
}

func (sp *StreamParser) pushback(line []byte) {
	sp.ccount -= int64(len(line))
	sp.importLine--
	sp.linebuffers = append(sp.linebuffers, line)
}

// Helpers for import-stream files

func (sp *StreamParser) fiReadline() []byte {
	// Read a line, stashing comments as we go.
	for {
		line := sp.readline()
		if len(line) > 0 && bytes.HasPrefix(line, []byte("#")) && !bytes.HasPrefix(line, []byte("#legacy-id")) {
			sp.repo.addEvent(newPassthrough(sp.repo, string(line)))
			if bytes.HasPrefix(line, []byte("#reposurgeon")) {
				// Extension command generated by some exporter's
				// --reposurgeon mode.
				fields := strings.Fields(string(line))
				if fields[1] == "sourcetype" && len(fields) == 3 {
					sp.repo.hint("", fields[2], true)
				}
			}
			continue
		} else {
			return line
		}
	}
}

func (sp *StreamParser) fiReadData(line []byte) ([]byte, int64) {
	// Read a fast-import data section.
	if len(line) == 0 {
		line = sp.fiReadline()
	}
	data := make([]byte, 0)
	var start int64
	if bytes.HasPrefix(line, []byte("data <<")) {
		delim := line[7:]
		start = sp.ccount
		for {
			dataline := sp.readline()
			if string(dataline) == string(delim) {
				break
			} else if len(dataline) == 0 {
				sp.error("EOF while reading blob")
			} else {
				data = append(data, dataline...)
			}
		}
	} else if bytes.HasPrefix(line, []byte("data")) {
		count, err := strconv.Atoi(strings.TrimSpace(string(line[5:])))
		if err != nil {
			sp.error("bad count in data: " + string(line[5:]))
		}
		start = sp.ccount
		data = sp.read(count)
	} else if bytes.HasPrefix(line, []byte("property")) {
		line = line[9:]                          // Skip this token
		line = line[bytes.IndexByte(line, ' '):] // Skip the property name
		nextws := bytes.IndexByte(line, ' ')
		count, err := strconv.Atoi(strings.TrimSpace(string(line[:nextws-1])))
		if err != nil {
			sp.error("bad count in property")
		}
		start = sp.ccount
		buf := sp.read(count)
		data = append(line[nextws:], buf...)
	} else {
		sp.error(fmt.Sprintf("malformed data header %q", line))
	}
	line = sp.readline()
	if string(line) != "\n" {
		sp.pushback(line) // Data commands optionally end with LF
	}
	return data, start
}

func (sp *StreamParser) fiParseFileop(fileop *FileOp) {
	// Read a fast-import fileop
	if fileop.ref[0] == ':' {
		return
	} else if fileop.ref == "inline" {
		data, _ := sp.fiReadData([]byte{})
		fileop.inline = data
	} else {
		sp.error("unknown content type in filemodify")
	}
}

// General helpers

func parseInt(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(throw("parse", "malformed integer literal: %v", err))
	}
	return n
}

func (sp *StreamParser) parseFastImport(options stringSet, baton *Baton, filesize int64) {
	// Beginning of fast-import stream parsing
	commitcount := 0
	branchPosition := make(map[string]*Commit)
	baton.startProgress("parse fast import stream", uint64(filesize))
	for {
		line := sp.fiReadline()
		if len(line) == 0 {
			break
		} else if len(bytes.TrimSpace(line)) == 0 {
			continue
		} else if bytes.HasPrefix(line, []byte("blob")) {
			blob := newBlob(sp.repo)
			line = sp.fiReadline()
			if bytes.HasPrefix(line, []byte("mark")) {
				sp.repo.markseq++
				blob.setMark(strings.TrimSpace(string(line[5:])))
			} else {
				sp.error("missing mark after blob")
			}
			line = sp.fiReadline()
			if bytes.HasPrefix(line, []byte("original-oid")) {
				blob.hash = newGitHash(bytes.Fields(line)[1])
			} else {
				sp.pushback(line)
			}
			blobcontent, blobstart := sp.fiReadData([]byte{})
			blob.setContent(blobcontent, blobstart)
			if cookie := blob.parseCookie(string(blobcontent)); cookie != nil {
				sp.lastcookie = *cookie
			}
			sp.repo.addEvent(blob)
			baton.twirl()
		} else if bytes.HasPrefix(line, []byte("data")) {
			sp.error("unexpected data object")
		} else if bytes.HasPrefix(line, []byte("commit")) {
			baton.twirl()
			commitbegin := sp.importLine
			commit := newCommit(sp.repo)
			commit.setBranch(strings.Fields(string(line))[1])
			for {
				line = sp.fiReadline()
				if len(line) == 0 {
					break
				} else if bytes.HasPrefix(line, []byte("original-oid")) {
					fmt.Sscan(string(bytes.Fields(line)[1]), "%x", commit.hash)
				} else if bytes.HasPrefix(line, []byte("#legacy-id")) {
					// reposurgeon extension, expected to
					// be immediately after "commit" if present
					commit.legacyID = string(bytes.Fields(line)[1])
					if sp.repo.vcs != nil {
						sp.repo.legacyMap[strings.ToUpper(sp.repo.vcs.name)+":"+commit.legacyID] = commit
					} else {
						sp.repo.legacyMap[commit.legacyID] = commit
					}
				} else if bytes.HasPrefix(line, []byte("mark")) {
					sp.repo.markseq++
					commit.setMark(string(bytes.TrimSpace(line[5:])))
				} else if bytes.HasPrefix(line, []byte("author")) {
					attrib, err := newAttribution(string(line[7:]))
					if err != nil {
						panic(throw("parse", "in author field: %v", err))
					}
					commit.authors = append(commit.authors, *attrib)
					sp.repo.tzmap[attrib.email] = attrib.date.timestamp.Location()
				} else if bytes.HasPrefix(line, []byte("committer")) {
					attrib, err := newAttribution(string(line[10:]))
					if err != nil {
						panic(throw("parse", "in committer field: %v", err))
					}
					commit.committer = *attrib
					sp.repo.tzmap[attrib.email] = attrib.date.timestamp.Location()
				} else if bytes.HasPrefix(line, []byte("property")) {
					newprops := newOrderedMap()
					commit.properties = &newprops
					fields := bytes.Split(line, []byte(" "))
					if len(fields) < 3 {
						sp.error("malformed property line")
					} else if len(fields) == 3 {
						commit.properties.set(string(fields[1]), "true")
					} else {
						name := fields[1]
						length := parseInt(string(fields[2]))
						value := bytes.Join(fields[3:], []byte(" "))
						if len(value) < length {
							value = append(value, sp.read(length-len(value))...)
							if sp.read(1)[0] != control.lineSep[0] {
								sp.error("trailing junk on property value")
							}
						} else if len(value) == length+1 {
							value = value[:len(value)-1] // Trim '\n'
						} else {
							value = append(value, sp.read(length-len(value))...)
							if sp.read(1)[0] != control.lineSep[0] {
								sp.error("newline not found where expected")
							}
						}
						commit.properties.set(string(name), string(value))
						// Generated by cvs-fast-export
						if string(name) == "cvs-revisions" {
							if !sp.repo.stronghint {
								if logEnable(logSHOUT) {
									logit("cvs_revisions property hints at CVS.")
								}
							}
							sp.repo.hint("cvs", "", true)
							scanner := bufio.NewScanner(bytes.NewReader(value))
							for scanner.Scan() {
								line := scanner.Text()
								if line != "" {
									sp.repo.legacyMap["CVS:"+line] = commit
								}
							}
						}
					}
				} else if bytes.HasPrefix(line, []byte("data")) {
					d, _ := sp.fiReadData(line)
					commit.Comment = string(d)
					if control.flagOptions["canonicalize"] {
						commit.Comment = canonicalizeComment(commit.Comment)
					}
				} else if bytes.HasPrefix(line, []byte("from")) || bytes.HasPrefix(line, []byte("merge")) {
					mark := string(bytes.Fields(line)[1])
					if isCallout(mark) {
						commit.addCallout(mark)
					} else {
						commit.addParentByMark(mark)
					}
				} else if line[0] == 'C' || line[0] == 'D' || line[0] == 'R' {
					commit.appendOperation(newFileOp(sp.repo).parse(string(line)))
				} else if string(line) == "deleteall\n" {
					commit.appendOperation(newFileOp(sp.repo).parse(string(line)))
				} else if line[0] == opM {
					fileop := newFileOp(sp.repo).parse(string(line))
					if fileop.ref != "inline" {
						ref := sp.repo.markToEvent(fileop.ref)
						if ref != nil {
							ref.(*Blob).appendOperation(fileop)
						} else {
							// Crap out on
							// anything
							// but a
							// submodule
							// link.
							if fileop.mode != "160000" {
								sp.error(fmt.Sprintf("ref %s could not be resolved", fileop.ref))
							}
						}
					}
					if fileop.mode == "160000" {
						// This is a submodule
						// link.  The ref
						// field is a SHA1
						// hash and the path is
						// an external
						// reference name.
						// Don't try to
						// collect data, just
						// pass it through.
						//sp.warn("submodule link")
					} else {
						// 100644, 100755, 120000.
						sp.fiParseFileop(fileop)
					}
					commit.appendOperation(fileop)
				} else if line[0] == opN {
					fileop := newFileOp(sp.repo).parse(string(line))
					commit.appendOperation(fileop)
					sp.fiParseFileop(fileop)
					sp.repo.inlines++
				} else if len(bytes.TrimSpace(line)) == 0 {
					// This handles slightly broken
					// exporters like the bzr-fast-export
					// one that may tack an extra LF onto
					// the end of data objects.  With it,
					// we don't drop out of the
					// commit-processing loop until we see
					// a *nonblank* line that doesn't match
					// a commit subpart.
					continue
				} else {
					// Dodgy bzr autodetection hook...
					if sp.repo.vcs == nil {
						if commit.hasProperties() && commit.properties.has("branch-nick") {
							sp.repo.hint("", "bzr", true)
						}
					}
					sp.pushback(line)
					break
				}
				baton.twirl()
			}
			if !(commit.mark != "" && commit.committer.fullname != "") {
				sp.importLine = commitbegin
				sp.error("missing required fields in commit")
			}
			if commit.mark == "" {
				sp.warn("unmarked commit")
			}
			if p, ok := branchPosition[commit.Branch]; ok && !commit.hasParents() {
				commit.addParentCommit(p)
				commit.implicitParent = true
			}
			sp.repo.addEvent(commit)
			branchPosition[commit.Branch] = commit
			commitcount++
			baton.twirl()
		} else if bytes.HasPrefix(line, []byte("reset")) {
			reset := newReset(sp.repo, "", "", "")
			reset.ref = string(bytes.TrimSpace(line[6:]))
			line = sp.fiReadline()
			if bytes.HasPrefix(line, []byte("from")) {
				committish := string(bytes.TrimSpace(line[5:]))
				reset.remember(sp.repo, committish)
				if commit, ok := sp.repo.markToEvent(committish).(*Commit); ok {
					branchPosition[reset.ref] = commit
				} else {
					if logEnable(logWARN) {
						logit("non-mark committish in reset")
					}
					delete(branchPosition, reset.ref)
				}
			} else {
				delete(branchPosition, reset.ref)
				sp.pushback(line)
			}
			sp.repo.addEvent(reset)
			baton.twirl()
		} else if bytes.HasPrefix(line, []byte("tag")) {
			var tagger *Attribution
			tagname := string(bytes.TrimSpace(line[4:]))
			line = sp.fiReadline()
			legacyID := ""
			if bytes.HasPrefix(line, []byte("#legacy-id ")) {
				// reposurgeon extension, expected to
				// be immediately after "tag" line if
				// present
				legacyID = string(bytes.Fields(line)[1])
				line = sp.fiReadline()
			}
			var referent string
			if bytes.HasPrefix(line, []byte("from")) {
				referent = string(bytes.TrimSpace(line[5:]))
			} else {
				sp.error(fmt.Sprintf("missing 'from' field in tag %q", tagname))
			}
			line = sp.fiReadline()
			if bytes.HasPrefix(line, []byte("tagger")) {
				var err error
				tagger, err = newAttribution(string(line[7:]))
				if err != nil {
					panic(throw("parse", "in tagger field: %v", err))
				}
			} else {
				sp.warn(fmt.Sprintf("missing 'tagger' field after 'from' field in tag %s", tagname))
				sp.pushback(line)
			}
			d, _ := sp.fiReadData([]byte{})
			tag := newTag(sp.repo, tagname, referent, tagger, string(d))
			tag.legacyID = legacyID
			sp.repo.addEvent(tag)
		} else {
			// Simply pass through any line we do not understand.
			sp.repo.addEvent(newPassthrough(sp.repo, string(line)))
		}
		baton.percentProgress(uint64(sp.ccount))
		if control.readLimit > 0 && uint64(commitcount) >= control.readLimit {
			if logEnable(logSHOUT) {
				logit("read limit %d reached", control.readLimit)
			}
			break
		}
	}
	baton.endProgress()
	if control.readLimit > 0 && uint64(commitcount) < control.readLimit {
		panic(throw("parse", "EOF before readlimit."))
	}
	for _, event := range sp.repo.events {
		switch event.(type) {
		case *Reset:
			reset := event.(*Reset)
			if reset.committish != "" {
				commit := sp.repo.markToEvent(reset.committish).(*Commit)
				if commit == nil {

					sp.shout(fmt.Sprintf("unresolved committish in reset %s", reset.committish))
				}
				commit.attach(reset)
			}
		case *Tag:
			tag := event.(*Tag)
			if tag.committish != "" {
				commit := sp.repo.markToEvent(tag.committish).(*Commit)
				if commit == nil {
					sp.shout(fmt.Sprintf("unresolved committish in tag %s", tag.committish))
				}
				commit.attach(tag)
			}
		}
	}
	if !sp.lastcookie.isEmpty() {
		sp.repo.hint("", sp.lastcookie.implies(), false)
	}
}

//
// The main event
//

func (sp *StreamParser) fastImport(ctx context.Context, fp io.Reader, options stringSet, source string, baton *Baton) {
	// Initialize the repo from a fast-import stream or Subversion dump.
	defer func() {
		if e := catch("parse", recover()); e != nil {
			croak(e.message)
			nuke(sp.repo.subdir(""), fmt.Sprintf("import interrupted, removing %s", sp.repo.subdir("")))
		}
	}()

	sp.timeMark("start")
	var filesize int64
	sp.fp = bufio.NewReader(fp)
	fileobj, ok := fp.(*os.File)
	// Optimization: if we're reading from a plain stream dump,
	// no need to clone all the blobs.
	if ok && isfile(fileobj.Name()) {
		sp.repo.seekstream = fileobj
		filesize = getsize(sp.repo.seekstream.Name())
	}
	if source == "" && sp.repo.seekstream != nil {
		source = sp.repo.seekstream.Name()
	}
	sp.source = source
	//baton.startProcess(fmt.Sprintf("reposurgeon: from %s", source), "")
	sp.repo.legacyCount = 0
	// First, determine the input type
	line := sp.readline()
	rate := func(count int) string {
		if baton != nil {
			elapsed := time.Since(baton.progress.start)
			ratek := int(float64(elapsed) / float64(count*1000))
			if ratek > 10e6 {
				// Can sometimes happen on small repos.
				// Say nothing rather than emitting a silly number.
				return ""
			}
			fmt.Sprintf(" (%dK/s)", ratek)
		}
		return ""
	}
	if bytes.HasPrefix(line, []byte("SVN-fs-dump-format-version: ")) {
		body := string(sdBody(line))
		if body != "1" && body != "2" {
			sp.error("unsupported dump format version " + body)
		}
		// Beginning of Subversion dump parsing
		sp.parseSubversion(ctx, &options, baton, filesize)
		// End of Subversion dump parsing
		sp.repo.vcs = svntype
		if control.flagOptions["progress"] && baton.progressEnabled {
			baton.printLogString(fmt.Sprintf("%d svn revisions%s",
				sp.repo.legacyCount, rate(sp.repo.legacyCount*1000)))
		}
	} else {
		sp.pushback(line)
		sp.parseFastImport(options, baton, filesize)
		sp.timeMark("parsing")
		if control.flagOptions["progress"] && baton.progressEnabled {
			if sp.repo.stronghint {
				baton.printLogString(fmt.Sprintf("%d %s events%s",
					len(sp.repo.events), sp.repo.vcs.name, rate(len(sp.repo.events))))
			} else {
				baton.printLogString(fmt.Sprintf("%d events%s",
					len(sp.repo.events), rate(len(sp.repo.events))))
			}
		}
	}
	//baton.endProcess()
	baton = nil
	sp.importLine = 0
	if len(sp.repo.events) == 0 {
		sp.error("ignoring empty repository")
	}

}

// Generic repository-manipulation code begins here

// Event is an operation in a repository's time sequence of modifications.
type Event interface {
	idMe() string
	getMark() string
	getComment() string
	String() string
	Save(io.Writer)
	moveto(*Repository)
	getDelFlag() bool
	setDelFlag(bool)
	isCommit() bool
}

// walkEvents walks an event list applying a hook function.
// This is intended to be parallelized.  Apply only when the
// computation has no dependency on the order in which commits
// are processed.
//
// Note: There's a clone of this code that walks selection sets.
// Go is not quite generic enough to make unifying the two convenient.
// We need to make sure they stay in sync.
func walkEvents(events []Event, hook func(int, Event)) {
	if control.flagOptions["serial"] {
		for i, e := range events {
			hook(i, e)
		}
		return
	}

	var (
		maxWorkers = runtime.GOMAXPROCS(0)
		channel    = make(chan int, maxWorkers)
		done       = make(chan bool, maxWorkers)
	)

	// Create the workers that will loop though events
	for n := 0; n < maxWorkers; n++ {
		go func() {
			// The for loop will stop when channel is closed
			for i := range channel {
				hook(i, events[i])
			}
			done <- true
		}()
	}

	// Populate the channel with the events
	for i := range events {
		channel <- i
	}
	close(channel)

	// Wait for all workers to finish
	for n := 0; n < maxWorkers; n++ {
		<-done
	}
}

// Safecounter is the simplest possible thread-safe counter,
// to be used inside walkEvents.
type Safecounter struct {
	sync.Mutex
	value int
}

func (c *Safecounter) bump() {
	c.Lock()
	c.value++
	c.Unlock()
}

// CommitLike is a Commit or Callout
type CommitLike interface {
	idMe() string
	getMark() string
	hasChildren() bool
	children() []CommitLike
	getComment() string
	callout() string
	String() string
	Save(io.Writer)
	moveto(*Repository)
	getDelFlag() bool
	setDelFlag(bool)
	getColor() colorType
	setColor(colorType)
	isCommit() bool
}

// Contributor - associate a username with a DVCS-style ID and timezone
type Contributor struct {
	local    string
	fullname string
	email    string
	timezone string
}

// ContributorID identifies a contributor for purposes of aliasing
type ContributorID struct {
	fullname string
	email    string
}

func (c Contributor) isEmpty() bool {
	return c.local == ""
}

func (c ContributorID) resolve(repo *Repository) ContributorID {
	for {
		found, ok := repo.aliases[c]
		if ok && !((c.fullname == "" || c.fullname == found.fullname) && c.email == found.email) {
			c = repo.aliases[c]
			continue
		}
		break
	}
	return c
}

// TimeMark is an elapsed-time record for profiling
type TimeMark struct {
	label string
	stamp time.Time
}

// Repository is the entire state of a version-control repository
type Repository struct {
	name             string
	readtime         time.Time
	vcs              *VCS
	stronghint       bool
	hintlist         []Hint
	sourcedir        string
	seekstream       *os.File
	events           []Event // A list of the events encountered, in order
	_markToIndex     map[string]int
	_markToIndexLen  int  // Cache is valid for events[:_markToIndexLen]
	_markToIndexSawN bool // whether we saw a null mark blob/commit when caching
	_markToIndexLock sync.Mutex
	_namecache       map[string][]int
	preserveSet      orderedStringSet
	basedir          string
	uuid             string
	writeLegacy      bool
	dollarMap        sync.Map // From dollar cookies in files
	dollarOnce       sync.Once
	legacyMap        map[string]*Commit // From anything that doesn't survive rebuild
	legacyCount      int
	timings          []TimeMark
	assignments      map[string]orderedIntSet
	inlines          int
	uniqueness       string // "committer_date", "committer_stamp", or ""
	markseq          int
	authormap        map[string]Contributor
	tzmap            map[string]*time.Location // most recent email address to timezone
	aliases          map[ContributorID]ContributorID
	maplock          sync.Mutex
	// Write control - set, if required, before each dump
	preferred      *VCS               // overrides vcs slot for writes
	realized       map[string]bool    // clear and remake this before each dump
	branchPosition map[string]*Commit // clear and remake this before each dump
	writeOptions   stringSet          // options requested on this write
	internals      orderedStringSet   // export code computes this itself
}

func newRepository(name string) *Repository {
	repo := new(Repository)
	repo.name = name
	repo.readtime = time.Now()
	repo.hintlist = make([]Hint, 0)
	repo.preserveSet = newOrderedStringSet()
	repo.legacyMap = make(map[string]*Commit)
	repo.assignments = make(map[string]orderedIntSet)
	repo.timings = make([]TimeMark, 0)
	repo.authormap = make(map[string]Contributor)
	repo.tzmap = make(map[string]*time.Location)
	repo.aliases = make(map[ContributorID]ContributorID)
	d, err := os.Getwd()
	if err != nil {
		panic(throw("command", "During repository creation: %v", err))
	}
	repo.basedir = d
	return repo
}

func (repo *Repository) subdir(name string) string {
	if name == "" {
		name = repo.name
	}
	head := fmt.Sprintf("%s/.rs%d", repo.basedir, os.Getpid())
	if name != "" {
		head += "-" + name
	}
	return filepath.FromSlash(head)
}

// cleanup releases disk storage associated with this repo
func (repo *Repository) cleanup() {
	nuke(repo.subdir(""),
		fmt.Sprintf("reposurgeon: cleaning up %s", repo.subdir("")))
}

// markToEvent finds an object by mark
func (repo *Repository) markToEvent(mark string) Event {
	idx := repo.markToIndex(mark)
	if idx != -1 {
		return repo.events[idx]
	}
	return nil
}

// returns the index of the specified object in the main event list
func (repo *Repository) eventToIndex(obj Event) int {
	mark := obj.getMark()
	if len(mark) != 0 {
		ind := repo.markToIndex(mark)
		if ind >= 0 {
			return ind
		}
	}
	for ind, event := range repo.events {
		if event == obj {
			return ind
		}
	}
	// Alas, we can't use Id() here without infinite recursion
	panic(fmt.Sprintf("Internal error: object %q not matched in repository %s", fmt.Sprintf("%v", obj), repo.name))
}

// MarkSettable is an interface declaring that an event has a mutable mark
type MarkSettable interface {
	setMark(string)
}

// gets an object index from its mark, or -1 if not found
func (repo *Repository) markToIndex(mark string) int {
	if mark == "" {
		return -1
	}
	repo._markToIndexLock.Lock()
	defer repo._markToIndexLock.Unlock()
	if index, ok := repo._markToIndex[mark]; ok {
		return index
	}
	L := len(repo.events)
	if repo._markToIndexLen < L {
		if repo._markToIndex == nil {
			// Most events are Blobs and Commits and can thus be searched
			// by mark. Use the event count as a hint to avoid growing the
			// map a lot of times after an invalidation.
			repo._markToIndex = make(map[string]int, len(repo.events))
		}
		for i := repo._markToIndexLen; i < L; i++ {
			event := repo.events[i]
			seenMark := event.getMark()
			if seenMark == "" {
				if _, ok := event.(MarkSettable); ok {
					// Remember we saw a null mark for an event
					// whose mark can be set, so that we know
					// we cannot avoid invalidation in setMark
					repo._markToIndexSawN = true
				}
			} else {
				repo._markToIndex[seenMark] = i
				if seenMark == mark {
					repo._markToIndexLen = i + 1
					return i
				}
			}
		}
		repo._markToIndexLen = L
	}
	return -1
}

func (repo *Repository) invalidateMarkToIndex() {
	repo._markToIndexLock.Lock()
	repo._markToIndex = nil
	repo._markToIndexLen = 0
	repo._markToIndexSawN = false
	repo._markToIndexLock.Unlock()
}

func (repo *Repository) fixupMarkToIndex(event Event, oldmark, newmark string) {
	if oldmark == "" {
		// maybe we are in events[:_markToIndexLen],
		// but since we had no mark we couldn't be in
		// the cache. We thus need to invalidate,
		// unless no such event was seen when caching.
		if repo._markToIndexSawN {
			repo.invalidateMarkToIndex()
		}
	} else if index, ok := repo._markToIndex[oldmark]; ok {
		if event != repo.events[index] {
			if logEnable(logSHOUT) {
				logit("Multiple events with the same mark corrupted the cache")
			}
			repo.invalidateMarkToIndex()
			return
		}
		repo._markToIndex[newmark] = index
		delete(repo._markToIndex, oldmark)
	}
	// If we get here, the old mark has not been found and the event
	// is thus guaranteed to be in the latter part of the event list,
	// where the mark to index is not made yet. Nothing to fixup.
}

func (repo *Repository) newmark() string {
	repo.markseq++
	mark := ":" + fmt.Sprintf("%d", repo.markseq)
	return mark
}

func (repo *Repository) makedir(legend string) {
	target := repo.subdir("")
	if logEnable(logSHUFFLE) {
		logit("repository %s creates %s", legend, target)
	}
	if _, err1 := os.Stat(target); os.IsNotExist(err1) {
		err2 := os.Mkdir(target, userReadWriteSearchMode)
		if err2 != nil {
			panic("Can't create repository directory")
		}
	} else if err1 != nil {
		panic(fmt.Errorf("Can't stat %s: %v", target, err1))
	}
}

// Hint is a hint about what kind of VCS we're in from looking at magic cookies.
type Hint struct {
	cookie string
	vcs    string
}

func (repo *Repository) hint(clue1 string, clue2 string, strong bool) bool {
	// Hint what the source of this repository might be.
	newhint := false
	for _, item := range repo.hintlist {
		if item.cookie == clue1 && item.vcs == clue2 {
			newhint = false
			break
		}
	}
	if newhint && repo.stronghint && strong {
		if logEnable(logSHOUT) {
			logit("new hint %s conflicts with old %s", clue1, repo.hintlist[len(repo.hintlist)-1])
		}
		return false
	}
	if !repo.stronghint && clue2 != "" {
		repo.vcs = findVCS(clue2)
	}
	if newhint {
		repo.hintlist = append(repo.hintlist, Hint{clue1, clue2})
	}
	notify := newhint && !repo.stronghint
	repo.stronghint = repo.stronghint || strong
	return notify
}

func (repo *Repository) size() int {
	// Return the size of this import stream, for statistics display.
	var sz int
	for _, e := range repo.events {
		sz += len(e.String())
	}
	return sz
}

func (repo *Repository) branchset() orderedStringSet {
	// branchset returns a set of all branchnames appearing in this repo.
	branches := newOrderedStringSet()
	for _, commit := range repo.commits(nil) {
		branches.Add(commit.Branch)
	}
	return branches
}

func (repo *Repository) branchmap() map[string]string {
	// Return a map of branchnames to terminal marks in this repo.
	brmap := make(map[string]string)
	for _, e := range repo.events {
		switch e.(type) {
		case *Reset:
			if e.(*Reset).committish == "" {
				delete(brmap, e.(*Reset).ref)
			} else {
				brmap[e.(*Reset).ref] = e.(*Reset).committish
			}
		case *Commit:
			brmap[e.(*Commit).Branch] = e.(*Commit).mark
		}
	}
	return brmap
}

func (repo *Repository) all() orderedIntSet {
	// Return a set that selects the entire repository.
	s := make([]int, len(repo.events))
	for i := range repo.events {
		s[i] = i
	}
	return s
}

func (repo *Repository) _buildNamecache() {
	// Avoid repeated O(n**2) lookups.
	repo._namecache = make(map[string][]int)
	commitcount := 0
	addOrAppend := func(index int, id string) {
		if _, ok := repo._namecache[id]; !ok {
			repo._namecache[id] = []int{index}
		} else {
			repo._namecache[id] = append(repo._namecache[id], index)
		}
	}
	for i, event := range repo.events {
		switch event.(type) {
		case *Commit:
			commitcount++
			repo._namecache[fmt.Sprintf("#%d", commitcount)] = []int{i}
			commit := event.(*Commit)
			legacyID := commit.legacyID
			if legacyID != "" {
				repo._namecache[legacyID] = []int{i}
			}

			committerStamp := commit.committer.actionStamp()
			var authorStamp string
			if len(commit.authors) > 0 {
				authorStamp = commit.authors[0].actionStamp()
				if authorStamp == committerStamp {
					continue
				}
				addOrAppend(i, authorStamp)
			}
			addOrAppend(i, committerStamp)
			// Ugh. We can't do this yet, it messes up roundtripping
			// of streams that didn't have OIDS.
			//addOrAppend(i, commit.gitHash().hexify())
			//addOrAppend(i, commit.gitHash().short())
		case *Tag:
			tag := event.(*Tag)
			repo._namecache[tag.tagname] = []int{i}
			if tag.legacyID != "" {
				repo._namecache[tag.legacyID] = []int{i}
			}
		case *Reset:
			reset := event.(*Reset)
			repo._namecache["reset@"+filepath.Base(reset.ref)] = []int{i}
			if reset.committish != "" {
				repo._namecache[filepath.Base(reset.ref)] = []int{repo.markToIndex(reset.committish)}
			}
			if reset.legacyID != "" {
				repo._namecache[reset.legacyID] = []int{i}
			}
		}
	}
}

func (repo *Repository) invalidateNamecache() {
	repo._namecache = nil
}

func (repo *Repository) named(ref string) orderedIntSet {
	// Resolve named reference in the control of this repository.
	selection := newOrderedIntSet()
	// For matches that require iterating across the entire event
	// sequence, building an entire name lookup table is !much
	// more expensive in time than doing a single lookup. Avoid
	// lots of O(n**2) searches by building a lookup cache, at the
	// expense of increased working set for the hash table.
	if repo._namecache == nil {
		repo._buildNamecache()
	}
	if v, ok := repo._namecache[ref]; ok {
		return v
	}
	// No hit in the name cache or assignments? Then search branches.
	for _, symbol := range repo.branchset() {
		if ref == branchbase(symbol) {
			loc := -1
			// Find the last commit with this branchname
			for i, event := range repo.events {
				switch event.(type) {
				case *Commit:
					if event.(*Commit).Branch == symbol {
						loc = i
					}
				}
			}
			if loc == -1 {
				if v, ok := repo._namecache["reset@"+ref]; ok {
					loc = repo.markToIndex(repo.events[v[0]].(*Reset).committish)
				}
			}
			if loc == -1 {
				panic(throw("command", "branch name %s points to hyperspace", symbol))
			} else {
				return newOrderedIntSet(loc)
			}
		}
	}
	// Next, assignments
	lookup, ok := repo.assignments[ref]
	if ok {
		return lookup
	}
	// Might be a date or action stamp (though action stamps should
	// be in the name cache already).  First, peel off an optional
	// ordinal suffix.
	var ordinal = -1
	stamp := ref
	m := regexp.MustCompile("#[0-9]+$").Find([]byte(ref))
	if m != nil {
		n, _ := strconv.Atoi(string(m[1:]))
		ordinal = n
		stamp = ref[:len(ref)-len(m)]
	}
	// Now look for action stamp or date
	dateEnd := len(stamp)
	bang := strings.Index(stamp, "!")
	if bang >= 0 {
		dateEnd = min(bang, dateEnd)
	}
	datestr := ref[:dateEnd]
	date, err2 := newDate(datestr)
	var datematch func(u Date) bool
	if err2 == nil {
		datematch = func(u Date) bool {
			return u.timestamp.Equal(date.timestamp)
		}
	} else {
		daymark, err3 := time.Parse("2006-01-02", datestr)
		if err3 == nil {
			datematch = func(u Date) bool {
				d := u.timestamp.Sub(daymark).Hours()
				return d >= 0 && d < 24
			}
		}
	}
	emailID := ""
	resolveAlias := func(a string) string {
		return ContributorID{"", a}.resolve(repo).email
	}
	if err2 == nil && bang > -1 {
		emailID = resolveAlias(ref[bang+1:])
	}
	matches := newOrderedIntSet()
	if datematch != nil {
		for ei, event := range repo.events {
			switch event.(type) {
			case *Commit:
				commit := event.(*Commit)
				if !datematch(commit.committer.date) {
					continue
				}
				if len(emailID) != 0 && resolveAlias(commit.committer.email) != emailID {
					continue
				} else {
					matches.Add(ei)
				}
			case *Tag:
				tag := event.(*Tag)
				if !datematch(tag.tagger.date) {
					continue
				} else if len(emailID) != 0 && resolveAlias(tag.tagger.email) != emailID {
					continue
				} else {
					matches.Add(ei)
				}
			}
		}
		if len(matches) < 1 {
			panic(throw("command", "no events match %s", ref))
		} else if len(matches) > 1 {
			if ordinal != -1 && ordinal <= len(matches) {
				selection.Add(matches[ordinal-1])
			} else {
				selection = selection.Union(matches)
			}
		} else {
			selection.Add(matches[0])
		}
		if len(selection) > 0 {
			return selection
		}
	}
	// More named-reference formats can go here.
	// Otherwise, return nil to signal invalid selection.
	return nil
}

func (repo *Repository) invalidateObjectMap() {
	repo.invalidateMarkToIndex()
}

func parseContributionLine(netwide string) (Contributor, *time.Location, error) {
	// Using parseAttrinutionLine here is a kludge that relies
	// on the fact that it doesn't interpret its third (timestamp)
	// field, so we can interpret it as a timezone spec instead.
	// That third field is actually the rest of the line after
	// the address and any interventing spaces; it is open to
	// other interpretations, including as multiple
	// fields with whitespace or other separators.
	name, mail, timezone, err := parseAttributionLine(netwide)
	var loc *time.Location
	if timezone != "" {
		loc, err = time.LoadLocation(timezone)
		if err != nil {
			loc, err = locationFromZoneOffset(timezone)
		}
	}
	return Contributor{"", name, mail, timezone}, loc, err
}

func (repo *Repository) readAuthorMap(selection orderedIntSet, fp io.Reader) error {
	// Read an author-mapping file and apply it to the repo.
	scanner := bufio.NewScanner(fp)
	var principal Contributor
	var loc *time.Location
	var err error
	var currentLineNumber uint64
	complain := func(msg string, args ...interface{}) {
		if logEnable(logSHOUT) {
			logit("in readAuthorMap, while parsing line %d: "+msg,
				append([]interface{}{currentLineNumber}, args)...)
		}
	}
	for scanner.Scan() {
		currentLineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			fields := strings.SplitN(line, "=", 3)
			local := strings.TrimSpace(fields[0])
			netwide := strings.TrimSpace(fields[1])
			principal, loc, err = parseContributionLine(netwide)
			principal.local = local
			if err != nil {
				complain("%v", err)
				continue
			}
			if principal.email == "" {
				complain("can't recognize address in '%s'", netwide)
				continue
			}
			if loc != nil {
				repo.tzmap[principal.email] = loc
			}
			key := strings.ToLower(local)
			repo.authormap[key] = principal
		}
		// Process aliases gathered from Changelog entries
		if line[0] == '+' {
			if principal.isEmpty() {
				complain("alias entry before any principal")
				continue
			}
			line = strings.TrimSpace(line[1:])
			alias, loc, aerr := parseContributionLine(line)
			if aerr != nil {
				complain("bad contributor alias: %v", aerr)
				continue
			}
			repo.aliases[ContributorID{alias.fullname, alias.email}] = ContributorID{principal.fullname, principal.email}
			if loc != nil {
				repo.tzmap[alias.email] = loc
			}
		}
	}

	repo.clrDelFlags()
	repo.walkEvents(selection, func(idx int, event Event) {
		switch event.(type) {
		case *Commit:
			c := event.(*Commit)
			c.committer.remap(repo.authormap)
			for ai := range c.authors {
				if c.authors[ai].remap(repo.authormap) {
					c.setDelFlag(true)
				}
			}
		case *Tag:
			event.(*Tag).tagger.remap(repo.authormap)
		}
	})
	// Email addresses have changed.
	// Force rebuild of action-stamp mapping on next lookup
	repo.invalidateNamecache()

	return nil
}

// List the identities we know.
func (repo *Repository) writeAuthorMap(selection orderedIntSet, fp io.Writer) error {
	contributors := make(map[string]string)
	for _, ei := range selection {
		event := repo.events[ei]
		switch event.(type) {
		case *Commit:
			commit := event.(*Commit)
			contributors[commit.committer.userid()] = commit.committer.who()
			for _, author := range commit.authors {
				contributors[author.userid()] = author.who()
			}
		case *Tag:
			tag := event.(*Tag)
			// A nil tagger field should never happen, but has been observed
			// in the wild when reading a Git repository file with corrupted
			// metadata: https://gitlab.com/esr/reposurgeon/issues/249
			// Skip the invalid tag.
			if tag.tagger != nil {
				contributors[tag.tagger.userid()] = tag.tagger.who()
			}
		}
	}
	for userid, cid := range contributors {
		_, err := fmt.Fprintf(fp, "%s = %s\n", userid, cid)
		if err != nil {
			return fmt.Errorf("in writeAuthorMap: %v", err)
		}
	}
	return nil
}

func (repo *Repository) byCommit(hook func(commit *Commit)) {
	for _, event := range repo.events {
		switch event.(type) {
		case *Commit:
			hook(event.(*Commit))
		}
	}
}

// Read a legacy-references dump and use it to initialize the repo's legacy map.
func (repo *Repository) readLegacyMap(fp io.Reader, baton *Baton) (int, int, error) {
	type dyad struct {
		a string
		b string
	}
	commitMap := make(map[dyad][]*Commit)
	repo.byCommit(func(commit *Commit) {
		key := dyad{commit.committer.date.timestamp.String(), commit.committer.email}
		if _, ok := commitMap[key]; !ok {
			commitMap[key] = make([]*Commit, 0)
		}
		commitMap[key] = append(commitMap[key], commit)
	})

	linecount := 0
	matched := 0
	unmatched := 0

	scanner := bufio.NewScanner(fp)
	for scanner.Scan() {
		linecount++
		line := scanner.Text()

		lineError := func(legend string) error {
			return fmt.Errorf(legend+": line %d %q\n", linecount, line)
		}

		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return matched, unmatched, lineError("bad line syntax in legacy map")
		}
		legacy, stamp := fields[0], fields[1]
		parts := strings.Split(stamp, "!")
		if len(fields) != 2 {
			return matched, unmatched, lineError("bad action stamp syntax in legacy map.")
		}
		var seq int
		var person, seqstr string
		timefield, person := parts[0], parts[1]
		if strings.Contains(person, ":") {
			fields = strings.SplitN(person, ":", 3)
			person, seqstr = fields[0], fields[1]
			d, err := strconv.Atoi(seqstr)
			if err != nil {
				return matched, unmatched, lineError("bad sequence number")
			}
			seq = d - 1
		} else {
			seq = 0
		}
		if legacy == "" || timefield == "" || person == "" {
			lineError("entry has blank fields")
		}
		when, err2 := newDate(timefield)
		if err2 != nil {
			return matched, unmatched, lineError(err2.Error())
		}
		whenWho := dyad{when.timestamp.String(), person}
		if _, ok := commitMap[whenWho]; ok {
			repo.legacyMap[legacy] = commitMap[whenWho][seq]
			if strings.HasPrefix(legacy, "SVN:") {
				commitMap[whenWho][seq].legacyID = legacy[4:]
			}
			matched++
		} else {
			unmatched++
		}
		//baton.twirl()
	}

	return matched, unmatched, nil
}

// commits returns a slice of the commits in a specified selection set
// or all commits if the selection set is nil.
func (repo *Repository) commits(selection orderedIntSet) []*Commit {
	out := make([]*Commit, 0)
	if selection == nil {
		for _, event := range repo.events {
			commit, ok := event.(*Commit)
			if ok {
				out = append(out, commit)
			}
		}
	} else {
		for _, idx := range selection {
			event := repo.events[idx]
			commit, ok := event.(*Commit)
			if ok {
				out = append(out, commit)
			}
		}
	}
	return out
}

func (repo *Repository) cleanLegacyMap() {
	newMap := make(map[string]*Commit)
	for key, commit := range repo.legacyMap {
		if commit.legacyID != "" && commit.mark != "" {
			i := repo.markToIndex(commit.mark)
			if i >= 0 && i < len(repo.events) && repo.events[i] == commit {
				newMap[key] = commit
			}
		}
	}
	repo.legacyMap = newMap
}

// Dump legacy references.
func (repo *Repository) writeLegacyMap(fp io.Writer, baton *Baton) error {
	keylist := make([]string, 0)
	repo.cleanLegacyMap()
	for key := range repo.legacyMap {
		keylist = append(keylist, key)
	}
	sort.Slice(keylist, func(i, j int) bool {
		ki := keylist[i]
		ci := repo.eventToIndex(repo.legacyMap[ki])
		kj := keylist[j]
		cj := repo.eventToIndex(repo.legacyMap[kj])
		return ci < cj || (ci == cj && ki < kj)
	})
	seen := map[string]int{}
	for _, cookie := range keylist {
		commit := repo.legacyMap[cookie]
		id := fmt.Sprintf("%s!%s",
			commit.committer.date.rfc3339(),
			commit.committer.email)
		serial := ""
		if seen[id] > 0 {
			serial += fmt.Sprintf(":%d", seen[id]+1)
		}
		seen[id]++
		fmt.Fprintf(fp, "%s\t%s%s\n", cookie, id, serial)
		//baton.twirl()
	}
	return nil
}

// Turn a commit into a tag.
func (repo *Repository) tagifyNoCheck(commit *Commit, name string, target string, legend string, delete bool, baton *Baton) {
	if logEnable(logEXTRACT) {
		commitID := commit.mark
		if commit.legacyID != "" {
			commitID += fmt.Sprintf(" <%s>", commit.legacyID)
		}
		if logEnable(logSHOUT) {
			logit(fmt.Sprintf("tagifying: %s -> %s", commitID, name))
		}
	}
	var pref string
	if commit.Comment == "" {
		pref = ""
	} else {
		pref = commit.Comment
		if legend != "" || !strings.HasSuffix(pref, control.lineSep) {
			pref += control.lineSep
		}
	}
	tag := newTag(commit.repo, name, target, &commit.committer, pref+legend)
	tag.legacyID = commit.legacyID
	repo.addEvent(tag)
	if delete {
		commit.delete([]string{"--tagback"}, baton)
	}
}

// Turn a commit into a tag.
func (repo *Repository) tagify(commit *Commit, name string, target string, legend string, delete bool, baton *Baton) {
	if len(commit.operations()) > 0 {
		panic("Attempting to tagify a commit with fileops.")
	}
	repo.tagifyNoCheck(commit, name, target, legend, delete, baton)
}

// Default scheme to name tags generated from empty commits
func defaultEmptyTagName(commit *Commit) string {
	if len(commit.operations()) > 0 {
		branch := commit.Branch
		if strings.HasSuffix(branch, svnSep) {
			branch = branch[:len(branch)-1]
		}
		return "tipdelete-" + branchbase(branch)
	}
	if commit.legacyID != "" {
		return "emptycommit-" + commit.legacyID
	} else if commit.mark != "" {
		return "emptycommit-mark" + commit.mark[1:]
	} else {
		return fmt.Sprintf("emptycommit-index%d", commit.index())
	}
}

/*
   func tagifyEmpty(self, commits = nil,
                          tipdeletes = false,
                          tagifyMerges = false,
                          canonicalize = true,
                          nameFunc = nil,
                          legendFunc = nil,
                          createTags = true,
                          gripe = complain
                         ):
           Arguments: * commits:       nil for all, or a set of event indices
                                       tagifyEmpty() ignores non-commits
                      * tipdeletes:    whether tipdeletes should be tagified
                      * canonicalize:  whether to canonicalize fileops first
                      * nameFunc:      custom function for choosing the tag
                                       name; if it returns an empty string,
                                       a default scheme is used
                      * legendFunc:    custom function for choosing the legend
                                       of a tag; no fallback is provided. By
                                       default it always returns "".
                      * createTags:    whether to create tags.
*/

func (repo *Repository) tagifyEmpty(selection orderedIntSet, tipdeletes bool, tagifyMerges bool, canonicalize bool, nameFunc func(*Commit) string, legendFunc func(*Commit) string, createTags bool, baton *Baton) error {
	// Turn into tags commits without (meaningful) fileops.
	// Use a separate loop because delete() invalidates manifests.
	if canonicalize {
		for _, commit := range repo.commits(selection) {
			commit.canonicalize()
		}
	}
	// Tagify commits without fileops
	var isTipdelete = func(commit *Commit) bool { return false }
	if tipdeletes {
		isTipdelete = func(c *Commit) bool {
			return c.alldeletes(deleteall) && !c.hasChildren()
		}
	}
	var errout error
	deletia := make([]int, 0)
	tagifyEvent := func(index int) {
		commit, ok := repo.events[index].(*Commit)
		if !ok {
			return
		}
		var name string
		if len(commit.operations()) == 0 || isTipdelete(commit) {
			if commit.hasParents() {
				if len(commit.parents()) > 1 && !tagifyMerges {
					return
				}
				if nameFunc != nil {
					name = nameFunc(commit)
					if name == "" {
						name = defaultEmptyTagName(commit)
					}
				} else {
					name = defaultEmptyTagName(commit)
				}
				//for repo.named(name) != nil {
				//	name += "-displaced"
				//}
				legend := ""
				if legendFunc != nil {
					legend = legendFunc(commit)
				}
				commit.setOperations(nil)
				if createTags {
					repo.tagify(commit,
						name,
						commit.parents()[0].getMark(),
						legend,
						false,
						baton)
				}
				deletia = append(deletia, index)
			} else {
				if commit.Branch != "refs/heads/master" {
					msg := ""
					if commit.legacyID != "" && repo.vcs != nil && repo.vcs.name == "svn" {
						msg += fmt.Sprintf(" r%s:", commit.legacyID)
					} else if commit.mark != "" {
						msg += fmt.Sprintf(" '%s':", commit.mark)
					}
					msg += " deleting parentless "
					if len(commit.operations()) > 0 {
						msg += fmt.Sprintf("tip delete of %s.", commit.Branch)
					} else {
						msg += fmt.Sprintf("zero-op commit on %s.", commit.Branch)
					}
					errout = errors.New(msg[1:])
					deletia = append(deletia, index)
				}
			}
		}
		baton.twirl()
	}

	if len(selection) == 0 {
		for index := range repo.events {
			tagifyEvent(index)
		}
	} else {
		for _, index := range selection {
			tagifyEvent(index)
		}
	}
	repo.delete(deletia, []string{"--tagback", "--no-preserve-refs"}, baton)
	return errout
}

// Read a stream file and use it to populate the repo.
func (repo *Repository) fastImport(ctx context.Context, fp io.Reader, options stringSet, source string, baton *Baton) {
	newStreamParser(repo).fastImport(ctx, fp, options, source, baton)
	repo.readtime = time.Now()
}

// Extract info about legacy references from CVS/SVN header cookies.
func (repo *Repository) parseDollarCookies() {
	// Set commit legacy properties from $Id$ && $Subversion$
	// cookies in blobs. In order to throw away stale headers from
	// after, e.g., a CVS-to-SVN or SVN-to-git conversion, we
	// ignore duplicate keys - note this assumes commits are properly
	// time-ordered, which is true for SVN but maybe not for
	// CVS. Might be we should explicitly time-check in the latter
	// case, but CVS timestamps aren't reliable so it might not
	// include conversion quality any.
	walkEvents(repo.events, func(idx int, event Event) {
		commit, iscommit := event.(*Commit)
		if !iscommit {
			return
		}
		for _, fileop := range commit.operations() {
			if fileop.op != opM {
				continue
			}
			blob, ok := repo.markToEvent(fileop.ref).(*Blob)
			if !ok {
				continue
			}
			if commit.hasProperties() && commit.properties.get("legacy") != "" {
				croak("legacy property of %s overwritten",
					commit.mark)
			}
			if blob.cookie != nil && blob.cookie.implies() == "SVN" {
				svnkey := "SVN:" + blob.cookie.rev
				repo.dollarMap.LoadOrStore(svnkey, commit)
			} else if blob.cookie != nil {
				if filepath.Base(fileop.Path) != blob.cookie.path {
					// Usually the
					// harmless result of
					// a file move or copy
					// that cvs2svn or
					// git-svn didn't pick
					// up on.
					croak("mismatched CVS header path '%s' in %s vs '%s' in %s",
						fileop.Path, commit.mark, blob.cookie.path, blob.mark)
				}
				cvskey := fmt.Sprintf("CVS:%s:%s", fileop.Path, blob.cookie.path)
				repo.dollarMap.LoadOrStore(cvskey, commit)
			}
		}
	})
}

// Audit the repository for uniqueness properties.
func (repo *Repository) checkUniqueness(chatty bool, logHook func(string)) {
	repo.uniqueness = ""
	timecheck := make(map[string]Event)
	timeCollisions := make(map[string][]Event)
	// Not worth parallelizing this loop, there isn't enough going on
	// outside of the actual mapn accesses - which must be locked and
	// serialized.
	commits := repo.commits(nil)
	for _, event := range commits {
		when := rfc3339(event.when())
		if _, recorded := timecheck[when]; recorded {
			if _, ok := timeCollisions[when]; !ok {
				timeCollisions[when] = []Event{}
			}
			timeCollisions[when] = append(timeCollisions[when], event)
		}
		timecheck[when] = event
	}
	if len(timeCollisions) == 0 {
		repo.uniqueness = "committer_date"
		if chatty {
			logHook("All commit times in this repository are unique.")
		}
		return
	}
	if logHook != nil {
		reps := make([]string, 0)
		for k := range timeCollisions {
			reps = append(reps, k)
		}
		logHook(fmt.Sprintf("These %d timestamps have multiple commits: %s",
			len(reps), strings.Join(reps, " ")))
	}
	stampCollisions := newOrderedStringSet()
	for _, clique := range timeCollisions {
		stampcheck := make(map[string]string)
		for _, event := range clique {
			commit, ok := event.(*Commit)
			if !ok {
				continue
			}
			stamp, ok := stampcheck[commit.actionStamp()]
			if ok {
				stampCollisions.Add(fmt.Sprintf("%s = %s", stamp, commit.idMe()))
			}
			stampcheck[stamp] = commit.mark
		}
	}
	if len(stampCollisions) == 0 {
		repo.uniqueness = "committer_stamp"
		if logHook != nil {
			logHook("All commit stamps in this repository are unique.")
		}
		return
	}
	if logHook != nil {
		logHook("These marks are in stamp collisions: " +
			strings.Join(stampCollisions, " "))
	}
}

// exportStyle says how we should we tune the export dump format.
func (repo *Repository) exportStyle() orderedStringSet {
	if repo.vcs != nil {
		return repo.vcs.styleflags
	}
	// Default to git style
	return orderedStringSet{"nl-after-commit"}
}

// Dump the repo object in Subversion dump or fast-export format.
func (repo *Repository) fastExport(selection orderedIntSet,
	fp io.Writer, options stringSet, target *VCS, baton *Baton) error {
	repo.writeOptions = options
	repo.preferred = target
	repo.internals = nil
	// Select all blobs implied by the commits in the range. If we ever
	// go to a representation where fileops are inline this logic will need
	// to be modified.
	if selection == nil {
		selection = repo.all()
	} else {
		repo.internals = newOrderedStringSet()
		for _, ei := range selection {
			event := repo.events[ei]
			if mark := event.getMark(); mark != "" {
				repo.internals.Add(mark)
			}
			if commit, ok := event.(*Commit); ok {
				for _, fileop := range commit.operations() {
					if fileop.op == opM {
						idx := repo.markToIndex(fileop.ref)
						if fileop.ref != "inline" {
							selection.Add(idx)
						}
					}
				}
				for _, tag := range commit.attachments {
					selection.Add(repo.eventToIndex(tag))
				}
			}
			baton.twirl()
		}
		selection.Sort()
	}
	repo.realized = make(map[string]bool)          // Track what branches are made
	repo.branchPosition = make(map[string]*Commit) // Track what branches are made
	baton.startProgress("export", uint64(len(repo.events)))
	for idx, ei := range selection {
		baton.twirl()
		event := repo.events[ei]
		if passthrough, ok := event.(*Passthrough); ok {
			// Support writing bzr repos to plain git if they don't
			// actually have the extension features their export
			// streams declare.  Without this check git fast-import
			// barfs on declarations for unused features.
			if strings.HasPrefix(passthrough.text, "feature") && !target.extensions.Contains(strings.Fields(passthrough.text)[1]) {
				continue
			}
		}
		if logEnable(logUNITE) {
			if event.getMark() != "" {
				if logEnable(logSHOUT) {
					logit(fmt.Sprintf("writing %d %s", ei, event.getMark()))
				}
			}
		}
		event.Save(fp)
		baton.percentProgress(uint64(idx) + 1)
	}
	baton.endProgress()
	repo.realized = nil
	repo.branchPosition = nil
	return nil
}

// Add a path to the preserve set, to be copied back on rebuild.
func (repo *Repository) preserve(filename string) error {
	if exists(filename) {
		repo.preserveSet.Add(filename)
	} else {
		return fmt.Errorf("%s doesn't exist", filename)
	}
	return nil
}

// Remove a path from the preserve set.
func (repo *Repository) unpreserve(filename string) error {
	if repo.preserveSet.Contains(filename) {
		repo.preserveSet.Remove(filename)
	} else {
		return fmt.Errorf("%s is not preserved", filename)
	}
	return nil
}

// Return the repo's preserve set.
func (repo *Repository) preservable() orderedStringSet {
	return repo.preserveSet
}

// Rename the repo.
func (repo *Repository) rename(newname string) error {
	// Can fail if the target directory exists.
	if exists(repo.subdir("")) {
		if logEnable(logSHUFFLE) {
			logit("repository rename %s->%s calls os.Rename(%q, %q)", repo.name, newname, repo.subdir(""), repo.subdir(newname))
		}
		err := os.Rename(repo.subdir(""), repo.subdir(newname))
		if err != nil {
			return fmt.Errorf("repo rename %s -> %s failed: %s", repo.subdir(""), repo.subdir(newname), err)
		}
	}
	repo.name = newname
	return nil
}

// Fast append avoids doing a full copy of the slice on every allocation
// Code trivially modified from AppendByte on "Go Slices: usage and internals".
func appendEvents(slice []Event, data ...Event) []Event {
	m := len(slice)
	n := m + len(data)
	if n > cap(slice) { // if necessary, reallocate
		// allocate double what's needed, for future growth.
		newSlice := make([]Event, max((n+1)*2, maxAlloc))
		copy(newSlice, slice)
		slice = newSlice
	}
	slice = slice[0:n]
	copy(slice[m:n], data)
	return slice
}

// Insert an event just before the specified index.
func (repo *Repository) insertEvent(event Event, where int, legend string) {
	// No-alloc insert: https://github.com/golang/go/wiki/SliceTricks
	repo.events = appendEvents(repo.events, nil)
	copy(repo.events[where+1:], repo.events[where:])
	repo.events[where] = event
	repo.declareSequenceMutation(legend)
}

func (repo *Repository) addEvent(event Event) {
	isDone := func(event Event) bool {
		passthrough, ok := event.(*Passthrough)
		return ok && passthrough.text == "done\n"
	}
	if len(repo.events) > 0 && isDone(repo.events[len(repo.events)-1]) {
		repo.events = append(repo.events, repo.events[len(repo.events)-1])
		repo.events[len(repo.events)-2] = event
	} else {
		repo.events = append(repo.events, event)
	}
}

// Filter assignments, warning if any of them goes empty.
func (repo *Repository) filterAssignments(f func(Event) bool) {
	intContains := func(list []int, val int) bool {
		for _, v := range list {
			if v == val {
				return true
			}
		}
		return false
	}
	for name, values := range repo.assignments {
		newassigns := make([]int, 0)
		dc := 0
		for i, e := range repo.events {
			if f(e) {
				dc++
			} else if intContains(values, i) {
				newassigns = append(newassigns, i-dc)
			}
		}
		if len(values) > 0 && len(newassigns) == 0 {
			croak(fmt.Sprintf("sequence modification left %s empty", name))
		}
		repo.assignments[name] = newassigns
	}
}

// Mark the repo event sequence modified.
func (repo *Repository) declareSequenceMutation(warning string) {
	repo.invalidateMarkToIndex()
	repo._namecache = nil
	if len(repo.assignments) > 0 && warning != "" {
		repo.assignments = nil
		croak("assignments invalidated by " + warning)
	}
}

// Return the earliest commit.
func (repo *Repository) earliestCommit() *Commit {
	for _, event := range repo.events {
		commit, ok := event.(*Commit)
		if ok {
			return commit
		}
	}
	panic(throw("command", "repository has no commits"))
}

// Return the date of earliest commit.
func (repo *Repository) earliest() time.Time {
	return repo.earliestCommit().committer.date.timestamp
}

// Return ancestors of an event, in reverse order.
func (repo *Repository) ancestors(ei int) orderedIntSet {
	trail := newOrderedIntSet()
	for {
		commit, ok := repo.events[ei].(*Commit)
		if !ok {
			panic(throw("command", "ancestors() reached non-commit event %d", ei))
		}
		if !commit.hasParents() {
			break
		} else {
			efrom := repo.markToIndex(commit.parentMarks()[0])
			trail.Add(efrom)
			ei = efrom
		}
	}
	return trail
}

// walkEvents walks a selection applying a hook function.to the events
// This method needs to be kept in sync with the walkEvents function.
func (repo *Repository) walkEvents(selection orderedIntSet, hook func(i int, event Event)) {
	if control.flagOptions["serial"] {
		for i, e := range selection {
			hook(i, repo.events[e])
		}
		return
	}

	var (
		maxWorkers = runtime.GOMAXPROCS(0)
		channel    = make(chan int, maxWorkers)
		done       = make(chan bool, maxWorkers)
	)

	// Create the workers that will loop though events
	for n := 0; n < maxWorkers; n++ {
		go func() {
			// The for loop will stop when channel is closed
			for i := range channel {
				hook(i, repo.events[selection[i]])
			}
			done <- true
		}()
	}

	// Populate the channel with the events
	for i := range selection {
		channel <- i
	}
	close(channel)

	// Wait for all workers to finish
	for n := 0; n < maxWorkers; n++ {
		<-done
	}
}

//
// Delete machinery begins here
//
// Count modifications of a path in this commit && its ancestors.
func (commit *Commit) ancestorCount(path string) int {
	count := 0
	for {
		for _, fileop := range commit.operations() {
			if fileop.op == opM && fileop.Path == path {
				count++
				break
			}
		}
		// 0, 1, && >1 are the interesting cases
		if count > 1 {
			return count
		}
		if commit.hasParents() {
			event := commit.parents()[0]
			lst, ok := event.(*Commit)
			if !ok {
				// Could be a Callout object
				break
			}
			commit = lst
		} else {
			break
		}
	}
	return count
}

// Simplify the list of file operations in this commit.
func (commit *Commit) simplify() {
	commit.discardOpsBeforeLastDeleteAll()
	// No need for a full PathMap here since no snapshot will ever be taken.
	// Use a simple map-backed PathMapLike, which is faster.
	visibleOps := newOrderedFlatPathMap()
	commit.applyFileOps(visibleOps, true, true)
	// Re-create the simplified fileops, passing any deleteadd through
	commit.remakeFileOps(visibleOps, true)
}

// Replay the fileops, keeping only the last operation. Rename and copy
// operations whose source is here are changed into the source operation
// with the new path, others are kept intact if keepUnresolvedOps is true,
// otherwise they are simply dropped. This removes any ordering dependency
// between operations.
func (commit *Commit) applyFileOps(presentOps PathMapLike,
	keepUnresolvedOps bool, keepDeleteOps bool) {
	myOps := commit.operations()
	// lastDeleteall is the index of the last deleteall or -1
	lastDeleteall := len(myOps) - 1
	for ; lastDeleteall >= 0; lastDeleteall-- {
		if myOps[lastDeleteall].op == deleteall {
			break
		}
	}
	if lastDeleteall >= 0 {
		// There is a deleteall, clear the present operations
		presentOps.clear()
	}
	doCopy := func(fileop *FileOp) bool {
		if prevop, ok := presentOps.get(fileop.Source); ok {
			newop := prevop.(*FileOp).Copy()
			newop.Path = fileop.Path
			presentOps.set(fileop.Path, newop)
			return true
		}
		return false
	}
	// Apply the fileops after the last deleteall
	bound := len(myOps)
	for i := lastDeleteall + 1; i < bound; i++ {
		fileop := myOps[i]
		switch fileop.op {
		case opM:
			presentOps.set(fileop.Path, fileop)
		case opD:
			if keepDeleteOps {
				presentOps.set(fileop.Path, fileop)
			} else {
				presentOps.remove(fileop.Path)
			}
		case opC:
			if !doCopy(fileop) && keepUnresolvedOps {
				presentOps.set(fileop.Path, fileop)
			}
		case opR:
			if doCopy(fileop) {
				presentOps.remove(fileop.Source)
			} else if keepUnresolvedOps {
				presentOps.set(fileop.Path, fileop)
			}
		}
	}
}

func (commit *Commit) remakeFileOps(visibleOps PathMapLike, withDeleteall bool) {
	// Sort the ops paths in a consistent way, inspired by git-fast-export
	// As it says, 'Handle files below a directory first, in case they are
	// all deleted and the directory changes to a file or symlink.'
	// Sort the deleteall first, the renames last, then sort lexicographically.
	// We check the directory depth to make sure that "a/b/c" < "a/b" < "a".
	paths := make([]string, visibleOps.size())
	i := 0
	countRC := 0
	visibleOps.iter(func(path string, iop interface{}) {
		fileop := iop.(*FileOp)
		paths[i] = path
		i++
		// Also count the number of RC ops to reserve space later
		if fileop.op == opR || fileop.op == opC {
			countRC++
		}
	})
	lessthan := func(i, j int) bool {
		left := paths[i]
		right := paths[j]
		if len(left) > len(right) {
			return left[:len(right)] <= right
		}
		return left < right[:len(left)]
	}
	sort.Slice(paths, lessthan)
	// Remake the fileop list with only the visible operations. There is an
	// order to respect: first the deleteall, then the R and C ops, because
	// the remaining ones have their source outside of the commit and any
	// previous M could pollute that source. M and D operations come last.
	var newOps []*FileOp
	posRC := 0
	posOther := countRC
	// Handle the deleteall
	oldOps := commit.operations()
	if withDeleteall && len(oldOps) > 0 && oldOps[0].op == deleteall {
		newOps = make([]*FileOp, len(paths)+1)
		newOps[0] = oldOps[0]
		posRC++
		posOther++
	} else {
		newOps = make([]*FileOp, len(paths))
	}
	// Handle the other ops
	for _, path := range paths {
		iop, _ := visibleOps.get(path)
		fileop := iop.(*FileOp)
		if fileop.op == opR || fileop.op == opC {
			newOps[posRC] = fileop
			posRC++
		} else {
			newOps[posOther] = fileop
			posOther++
		}
	}
	// Now replace the Commit fileops.
	commit.setOperationsNoInvalidate(newOps)
}

var allPolicies = orderedStringSet{
	"--complain",
	"--no-coalesce",
	"--delete",
	"--empty-only",
	"--pushback",
	"--pushforward",
	"--no-preserve-refs",
	"--tagback",
	"--tagforward",
	"--quiet",
	"--blobs",
}

func (repo *Repository) clrDelFlags() {
	walkEvents(repo.events, func(i int, event Event) { event.setDelFlag(false) })
}

func (repo *Repository) countDelFlags() int {
	count := 0
	for _, event := range repo.events {
		if event.getDelFlag() {
			count++
		}
	}
	return count
}

// Delete a set of events, or rearrange it forward or backwards.
func (repo *Repository) squash(selected orderedIntSet, policy orderedStringSet, baton *Baton) error {
	if logEnable(logDELETE) {
		logit("Deletion list is %v", selected)
	}
	for _, qualifier := range policy {
		if !allPolicies.Contains(qualifier) {
			return errors.New("no such deletion modifier as " + qualifier)
		}
	}
	// For --pushback, it is critical that deletions take place
	// from lowest event number to highest since --pushback often
	// involves re-ordering events even as it is iterating over
	// the "selected" list. Only events earlier than the event
	// currently being processed are re-ordered, however, so the
	// operation is safe as long as events are visited lowest to
	// highest. (For --pushforward, iteration order is immaterial
	// since it does no event re-ordering and actual deletion is
	// delayed until after iteration is finished.)
	selected.Sort()
	dquiet := policy.Contains("--quiet")
	delete := policy.Contains("--delete")
	preserveRefs := !policy.Contains("--no-preserve-refs")
	tagback := policy.Contains("--tagback")
	tagforward := policy.Contains("--tagforward") || (!delete && !tagback)
	pushback := policy.Contains("--pushback")
	pushforward := policy.Contains("--pushforward") || (!delete && !pushback)
	coalesce := !policy.Contains("--no-coalesce")
	delblobs := policy.Contains("--blobs")
	// Sanity checks
	if !dquiet {
		for _, ei := range selected {
			event := repo.events[ei]
			commit, ok := event.(*Commit)
			if !ok {
				continue
			}
			if delete {
				speak := fmt.Sprintf("warning: commit %s to be deleted has ", commit.mark)
				if strings.Contains(commit.Branch, "/") && !strings.Contains(commit.Branch, "/heads/") {
					if logEnable(logWARN) {
						logit(speak + fmt.Sprintf("non-head branch attribute %s", commit.Branch))
					}
				}
				if !commit.alldeletes(opD, deleteall) {
					if logEnable(logWARN) {
						logit(speak + "non-delete fileops.")
					}
				}
			}
			if !delete {
				if pushback && !commit.hasParents() {
					croak("warning: pushback of parentless commit %s", commit.mark)
				}
				if pushforward && !commit.hasChildren() {
					croak("warning: pushforward of childless commit %s", commit.mark)
				}
			}
		}
	}
	// A special check on the first commit is required when pushing back
	if pushback {
		for _, ei := range selected {
			event := repo.events[ei]
			commit, ok := event.(*Commit)
			if !ok {
				continue
			}
			// The case we  want to avoid is a first
			// parent that is also the first parent of
			// other commits. If werere to allow pushback
			// to it we'd have to compute an inverse
			// fileop and push it forward to the other
			// children.
			if len(commit.children()) > 1 {
				firstparent := 0
				for _, child := range commit.children() {
					if childcommit, ok := child.(*Commit); ok && childcommit.parents()[0] == commit {
						firstparent++
					}
				}
				if firstparent > 1 {
					croak("can't push back to a first parent tha is a multi-child commit")
				}
			}
		}
	}
	altered := make([]*Commit, 0)
	var branchmap map[string]string
	if preserveRefs {
		branchmap = repo.branchmap()
	}
	// Here are the deletions
	repo.clrDelFlags()
	var delCount int
	for _, ei := range selected {
		var newTarget *Commit
		event := repo.events[ei]
		switch event.(type) {
		case *Blob:
			event.setDelFlag(delblobs)
		case *Tag:
			event.setDelFlag(delete)
		case *Reset:
			event.setDelFlag(delete)
		case *Passthrough:
			event.setDelFlag(delete)
		case *Commit:
			fileopsWerePushed := false
			event.setDelFlag(true)
			delCount++
			commit := event.(*Commit)
			// Decide the new target for tags
			if tagforward && commit.hasChildren() {
				newTarget = commit.firstChild()
			} else if tagback && commit.hasParents() {
				noncallout, ok := commit.parents()[0].(*Commit)
				if ok {
					newTarget = noncallout
				}
			}
			if newTarget != nil {
				if logEnable(logDELETE) {
					logit("new target for tags and resets is %s", newTarget.getMark())
				}
			}
			// Reparent each child.  Concatenate comments,
			// ignoring empty-log-message markers.
			composeComment := func(a string, b string) string {
				if a == b {
					return a
				}
				aEmpty := emptyComment(a)
				bEmpty := emptyComment(b)
				if aEmpty && bEmpty {
					return ""
				} else if aEmpty && !bEmpty {
					return b
				} else if !aEmpty && bEmpty {
					return a
				}
				return a + control.lineSep + b
			}
			//if logEnable(logDELETE) {logit("deleting %s requires %v to be reparented.", commit.getMark(), commit.childMarks())}
			for _, cchild := range commit.childMarks() {
				if isCallout(cchild) {
					continue
				}
				child := repo.markToEvent(cchild).(*Commit)
				// Insert commit's parents in place of
				// commit in child's parent list. We
				// keep existing duplicates in case
				// they are wanted, <s>but ensure we
				// don't introduce new ones.</s> -
				// that was true in Python but no
				// longer unless it induces a bug.
				oldParents := child.parents()
				eventPos := 0
				for i, p := range oldParents {
					if p == commit {
						eventPos = i
						break
					}
				}
				//if logEnable(logDELETE) {logit("reparenting: %s", child.getMark())}
				// Start with existing parents before us,
				// including existing duplicates
				newParents := make([]CommitLike, len(oldParents)-1+len(commit.parents()))
				newParentIndex := 0
				newParentIndex += copy(newParents, oldParents[:eventPos])
				// Add our parents. The Python version
				// tossed out duplicates of preceding
				// parents.  Skip callouts.
				for _, ancestor := range commit.parents() {
					newParents[newParentIndex] = ancestor
					newParentIndex++
				}
				// In Python, we "Avoid duplicates due to
				// commit.parents() insertion." Requires some
				// odd contortions in Go so we won't do it
				// unless there's a bug case.
				if len(oldParents) > eventPos {
					copy(newParents[newParentIndex:], oldParents[eventPos+1:])
				}
				// Prepend a copy of this event's file ops to
				// all children with the event as their first
				// parent,and mark each such child as needing
				// resolution.
				if pushforward && child.parents()[0] == commit {
					myOperations := make([]*FileOp, len(commit.operations()))
					for i, op := range commit.operations() {
						myOperations[i] = op.Copy()
					}
					child.fileops = append(myOperations, child.fileops...)
					fileopsWerePushed = true
					// Also prepend event's
					// comment, ignoring empty log
					// messages.
					if policy.Contains("--empty-only") && !emptyComment(child.Comment) {
						croak(fmt.Sprintf("--empty is on and %s comment is nonempty", child.idMe()))
					}
					child.Comment = composeComment(commit.Comment,
						child.Comment)
					altered = append(altered, child)
				}
				// Really set the parents to the newly
				// constructed list
				if logEnable(logDELETE) {
					logit("Parents of %s changed from %v to %v",
						child.getMark(), listMarks(oldParents), listMarks(newParents))
				}
				child.setParents(newParents)
				// If event was the first parent of
				// child yet has no parents of its
				// own, then child's first parent has
				// changed.  Prepend a deleteall to
				// child's fileops to ensure it starts
				// with an empty tree (as event does)
				// instead of inheriting that of its
				// new first parent.
				if eventPos == 0 && !commit.hasParents() {
					fileop := newFileOp(repo)
					fileop.construct(deleteall)
					child.prependOperation(fileop)
					altered = append(altered, child)
				}
			}
			// OK, we're done manipulating commit's children.
			// We might be trying to hand the commit's
			// fileops to its primary parent.
			if pushback && commit.hasParents() {
				// Append a copy of this event's file
				// ops to its primary parent fileop
				// list and mark the parent as needing
				// resolution.
				cparent := commit.parents()[0]
				parent, ok := cparent.(*Commit)
				if !ok {
					continue // Ignore callouts
				}
				myOperations := make([]*FileOp, len(commit.operations()))
				for i, op := range commit.operations() {
					myOperations[i] = op.Copy()
				}
				parent.fileops = append(parent.fileops, myOperations...)
				fileopsWerePushed = true
				// Also append child"s comment to its parent"s
				if policy.Contains("--empty-only") && !emptyComment(parent.Comment) {
					croak(fmt.Sprintf("--empty is on and %s comment is nonempty", parent.idMe()))
				}
				parent.Comment = composeComment(parent.Comment,
					commit.Comment)
				altered = append(altered, parent)
				// We need to ensure all fileop blobs
				// are defined before the
				// corresponding fileop, in other
				// words ensure that the blobs appear
				// before the primary parent in the
				// stream.  Easiest way to do this is
				// shift the range of events between
				// commit and parent down and put
				// parent just before commit.
				earliest := parent.index()
				latest := commit.index()
				for i := earliest; i < latest; i++ {
					repo.events[i] = repo.events[i+1]
				}
				repo.events[latest-1] = parent
				repo.declareSequenceMutation("squash pushback")
			}

			// This is where reference counting pays off
			if !fileopsWerePushed {
				for _, op := range commit.operations() {
					op.forget()
				}
			}

			// Move tags && attachments
			if newTarget == nil {
				// No place to move alternatives, no alternative but to nuke them.
				for _, e := range commit.attachments {
					e.setDelFlag(true)
					delCount++
				}
			} else {
				// use a copy of attachments since it
				// will be mutated
				attachmentsCopy := make([]Event, 0)
				for _, e := range commit.attachments {
					attachmentsCopy = append(attachmentsCopy, e)
				}
				needReset := true
				for _, e := range attachmentsCopy {
					if logEnable(logDELETE) {
						logit("moving attachment %s of %s to %s", commit.mark, e.idMe(), newTarget.getMark())
					}
					switch object := e.(type) {
					case *Tag:
						// object is already cast to Tag
						if commit.Branch == "refs/tags/"+object.tagname {
							needReset = false
						}
						object.remember(repo, newTarget.getMark())
					case *Reset:
						// object is already cast to Reset
						if commit.Branch == object.ref {
							needReset = false
						}
						object.remember(repo, newTarget.getMark())
					}
				}
				// Preserve reference integrity. If we are deleting the last event moving
				// a ref, and newTarget is not on the same branch, then the ref will not
				// point to newTarget. If there is already a reset or tag that updates the
				// ref, it will have been moved already to newTarget. Otherwise, we need
				// to create one now.
				if preserveRefs && needReset && newTarget.Branch != commit.Branch &&
					commit.mark == branchmap[commit.Branch] {
					repo.addEvent(newReset(repo, commit.Branch,
						newTarget.mark, commit.legacyID))
				}
			}
			// And forget the deleted event
			commit.forget()
		}
	}
	// Preserve assignments
	repo.filterAssignments(func(e Event) bool { return e.getDelFlag() })
	// Do the actual deletions
	survivors := make([]Event, 0, len(repo.events)-delCount)
	for _, e := range repo.events {
		if e.getDelFlag() {
			continue
		}
		if b, ok := e.(*Blob); ok && len(b.opset) == 0 {
			continue
		}
		survivors = append(survivors, e)
	}
	repo.events = survivors
	repo.declareSequenceMutation("squash/delete")
	// Canonicalize all the commits that got ops pushed to them
	if coalesce {
		for _, commit := range altered {
			if commit.getDelFlag() {
				continue
			}
			if logEnable(logDELETE) {
				if logEnable(logDELETE) {
					logit("Before canonicalization:")
				}
				commit.fileopDump(baton)
			}
			commit.simplify()
			if logEnable(logDELETE) {
				if logEnable(logDELETE) {
					logit("After canonicalization:")
				}
				commit.fileopDump(baton)
			}
		}
	}

	// Cleanup
	repo.cleanLegacyMap()
	return nil
}

// Delete a set of events.
func (repo *Repository) delete(selected orderedIntSet, policy orderedStringSet, baton *Baton) {
	options := append(orderedStringSet{"--delete", "--quiet"}, policy...)
	repo.squash(selected, options, baton)
}

// Replace references to duplicate blobs according to the given dupMap,
// which maps marks of duplicate blobs to canonical marks`
func (repo *Repository) dedup(dupMap map[string]string, baton *Baton) {
	walkEvents(repo.events, func(idx int, event Event) {
		commit, ok := event.(*Commit)
		if !ok {
			return
		}
		for _, fileop := range commit.operations() {
			if fileop.op == opM && dupMap[fileop.ref] != "" {
				fileop.ref = dupMap[fileop.ref]
			}
		}
		baton.twirl()
	})
	repo.gcBlobs()
}

// Garbage-collect blobs that no longer have references.
// Note: if you find yourself using this you are probably
// doing down a bad path. It's generally better for whatever
// operation you are doing that might free blobs to finish
// with a squash() call that infokes the normal code path
// for cleaning up unreferenced blobs.
func (repo *Repository) gcBlobs() {
	backreferences := make(map[string]bool)
	for _, commit := range repo.commits(nil) {
		for _, fileop := range commit.operations() {
			if fileop.op == opM {
				backreferences[fileop.ref] = true
			}
		}
	}
	eligible := func(event Event) bool {
		blob, ok := event.(*Blob)
		return ok && !backreferences[blob.mark]
	}
	repo.filterAssignments(eligible)
	// Apply the filter-without-allocate hack from Slice Tricks
	newEvents := repo.events[:0]
	for _, x := range repo.events {
		if !eligible(x) {
			newEvents = append(newEvents, x)
		}
	}
	repo.events = newEvents
	repo.declareSequenceMutation("GC")
}

//
// Delete machinery ends here
//

// Expunge a set of files from the commits in the selection set.
func (repo *Repository) expunge(selection orderedIntSet, expunge *regexp.Regexp, delete bool, notagify bool, baton *Baton) error {
	// First pass: compute fileop deletions
	alterations := make([][]int, 0)
	repo.clrDelFlags()
	for _, ei := range selection {
		event := repo.events[ei]
		deletia := make([]int, 0)
		commit, ok := event.(*Commit)
		if ok {
			for i, fileop := range commit.operations() {
				if logEnable(logDELETE) {
					logit(fileop.String() + control.lineSep)
				}
				if fileop.op == opD || fileop.op == opM {
					if expunge.MatchString(fileop.Path) == delete {
						deletia = append(deletia, i)
					}
				} else if fileop.op == opR || fileop.op == opC {
					// FIXME: This code needs tests
					sourcedelete := expunge.MatchString(fileop.Source) == delete
					targetdelete := expunge.MatchString(fileop.Path) == delete
					if sourcedelete {
						deletia = append(deletia, i)
						if fileop.op == opR {
							oldmatchers := strings.Split(expunge.String(), "|")
							newmatchers := make([]string, 0)
							for _, m := range oldmatchers {
								if m != "^"+regexp.QuoteMeta(fileop.Source)+"$" {
									newmatchers = append(newmatchers, m)
								}
							}
							expunge = regexp.MustCompile(strings.Join(newmatchers, "|"))
						}
					} else if targetdelete {
						if fileop.op == opR {
							fileop.op = opD
						} else if fileop.op == opC {
							deletia = append(deletia, i)
						}
					}
				}
			}
		}
		alterations = append(alterations, deletia)
	}
	// Second pass: perform actual fileop expunges
	for i, ei := range selection {
		deletia := alterations[i]
		if len(deletia) == 0 {
			continue
		}
		commit := repo.events[ei].(*Commit)
		keepers := make([]*FileOp, 0)
		blobs := make([]*Blob, 0)
		for _, j := range deletia {
			fileop := commit.fileops[j]
			var sourcedelete bool
			var targetdelete bool
			if fileop.op == opD {
				keepers = append(keepers, fileop)
				respond("at %d, expunging D %s",
					ei+1, fileop.Path)
			} else if fileop.op == opM {
				keepers = append(keepers, fileop)
				if fileop.ref != "inline" {
					bi := repo.markToIndex(fileop.ref)
					blob := repo.events[bi].(*Blob)
					blob.removeOperation(fileop)
					//assert(isinstance(blob, Blob))
					blobs = append(blobs, blob)
				}
				respond("at %d, expunging M %s", ei+1, fileop.Path)
			} else if fileop.op == opR || fileop.op == opC {
				//assert(sourcedelete || targetdelete)
				if sourcedelete && targetdelete {
					keepers = append(keepers, fileop)
				}
			}
		}

		nondeletia := make([]*FileOp, 0)
		deletiaSet := newOrderedIntSet(deletia...)
		for i, op := range commit.operations() {
			if !deletiaSet.Contains(i) {
				nondeletia = append(nondeletia, op)
			}
		}
		commit.setOperations(nondeletia)
		commit.setDelFlag(true)
	}
	backreferences := make(map[string]int)
	for _, commit := range repo.commits(nil) {
		for _, fileop := range commit.operations() {
			if fileop.op == opM {
				backreferences[fileop.ref]++
			}
		}
	}
	// Now remove commits that no longer have fileops, and released blobs.
	// log events that will be deleted.
	if logEnable(logDELETE) {
		toDelete := make([]int, 0)
		for i, event := range repo.events {
			switch event.(type) {
			case *Blob:
				blob := event.(*Blob)
				if backreferences[blob.mark] == 0 {
					toDelete = append(toDelete, i+1)
				}
			case *Commit:
				commit := event.(*Commit)
				if len(commit.operations()) == 0 {
					toDelete = append(toDelete, i+1)
				}
			}
		}
		if len(toDelete) == 0 {
			respond("deletion set is empty.")
		} else {
			respond("deleting blobs and empty commits %v", toDelete)
		}
	}
	// First delete the blobs.  Use the SliceTricks idiom for filtering
	// in place so no additional allocation is required.
	filtered := repo.events[:0]
	for _, event := range repo.events {
		blob, ok := event.(*Blob)
		if !ok || backreferences[blob.mark] > 0 {
			filtered = append(filtered, event)
		}
	}
	repo.events = filtered
	repo.invalidateMarkToIndex()
	errout := repo.tagifyEmpty(nil, false, false, false, nil, nil, !notagify, baton)
	// And tell we changed the manifests and the event sequence.
	//repo.invalidateManifests()
	repo.declareSequenceMutation("expunge cleanup")
	return errout
}

// Return options and features.  Makes a copy slice.
func (repo *Repository) frontEvents() []Event {
	var front = make([]Event, 0)
	for _, event := range repo.events {
		passthrough, ok := event.(*Passthrough)
		if !ok {
			break
		}
		front = append(front, passthrough)
	}
	return front
}

// A DAG is a directed acyclic graph
type DAG map[int]*DAGedges

func (d *DAG) setdefault(key int, e *DAGedges) *DAGedges {
	_, ok := (*d)[key]
	if !ok {
		(*d)[key] = e
	}
	return (*d)[key]
}

// DAGedges is a set of in and out edges to be associated with a DAG
type DAGedges struct {
	eout orderedIntSet
	ein  orderedIntSet
}

func (d DAGedges) String() string {
	return fmt.Sprintf("<%v | %v>", d.ein, d.eout)
}

// From https://golang.org/pkg/container/heap/#example__intHeap

// An IntHeap is a min-heap of ints.
type IntHeap []int

func (h IntHeap) Len() int           { return len(h) }
func (h IntHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h IntHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

// Push pushes an int-like object onto the heap
func (h *IntHeap) Push(x interface{}) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*h = append(*h, x.(int))
}

// Pop pops an int-like object from the heap
func (h *IntHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// resort topologically sorts the events in this repository.
// It reorders self.events so that objects referenced by other objects
// appear first.  The sort is stable to avoid unnecessary churn.
func (repo *Repository) resort() {
	var dag DAG = make(map[int]*DAGedges)
	start := repo.all()

	// helper for constructing the dag
	handle := func(x int, ymark string) {
		//assert(ymark != nil)
		if ymark == "inline" {
			return
		}
		//assert(ymark[0] == ":")
		y := repo.markToIndex(ymark)
		//assert(y != nil)
		//assert(x != y)
		start.Remove(x)
		dag.setdefault(x, new(DAGedges)).eout.Add(y)
		dag.setdefault(y, new(DAGedges)).ein.Add(x)
	}

	// construct the DAG
	for n, node := range repo.events {
		edges := dag.setdefault(n, new(DAGedges))
		switch node.(type) {
		case *Commit:
			commit := node.(*Commit)
			for _, parent := range commit.parents() {
				p := repo.eventToIndex(parent)
				//assert(n != p)
				start.Remove(n)
				edges.eout.Add(p)
				dag.setdefault(p, new(DAGedges)).ein.Add(n)
			}
			for _, o := range commit.operations() {
				if o.op == opM {
					handle(n, o.ref)
				} else if o.op == opN {
					handle(n, o.ref)
					handle(n, o.committish)
				}
			}
		case *Blob:
			continue
		case *Tag:
			handle(n, node.(*Tag).committish)
		case *Reset:
			reset := node.(*Reset)
			if reset.committish != "" {
				t := repo.markToIndex(reset.committish)
				//assert(n != t)
				start.Remove(n)
				edges.eout.Add(t)
				dag.setdefault(t, new(DAGedges)).ein.Add(n)
			}
		case *Passthrough:
			continue
		default:
			panic("unexpected event type")

		}
	}
	// now topologically sort the dag, using a priority queue to
	// provide a stable topological sort (each event's priority is
	// its original index)
	s := new(IntHeap)
	heap.Init(s)
	for _, elt := range start {
		heap.Push(s, elt)
	}
	tsorted := newOrderedIntSet()
	oldIndexToNew := make(map[int]int)
	for len(*s) > 0 {
		n := heap.Pop(s).(int)
		//assert n not in old_index_to_new
		oldIndexToNew[n] = len(tsorted)
		tsorted.Add(n)
		ein := dag[n].ein
		for len(ein) > 0 {
			m := ein.Pop()
			medges := dag[m]
			medges.eout.Remove(n)
			if len(medges.eout) == 0 {
				heap.Push(s, m)
			}
		}
	}

	orig := repo.all()
	//assert len(t) == len(tsorted)
	if !tsorted.EqualWithOrdering(orig) {
		//fmt.Sprintf("Sort order: %v\n", tsorted)
		//assert len(t - o) == 0
		leftout := orig.Subtract(tsorted)
		if len(leftout) > 0 {
			croak("event re-sort failed due to one or more dependency cycles involving the following events: %v", leftout)
			return
		}
		newEvents := make([]Event, len(repo.events))
		for i, j := range tsorted {
			newEvents[i] = repo.events[j]
		}
		repo.events = newEvents
		respond("re-sorted events")
		// assignments will be fixed so don't pass anything to
		// declareSequenceMutation() to tell it to warn about
		// invalidated assignments
		repo.declareSequenceMutation("")
		for k, v := range repo.assignments {
			old := v
			repo.assignments[k] = make([]int, len(v))
			for i := range v {
				repo.assignments[k][i] = oldIndexToNew[old[i]]
			}
		}
	}
}

// Re-order a contiguous range of commits.
func (repo *Repository) reorderCommits(v []int, bequiet bool) {
	if len(v) <= 1 {
		return
	}
	events := make([]*Commit, len(v))
	for i, e := range v {
		commit, ok := repo.events[e].(*Commit)
		if ok {
			events[i] = commit
		}
	}
	sortedEvents := make([]*Commit, len(v))
	sort.Sort(sort.IntSlice(v))
	for i, e := range v {
		commit, ok := repo.events[e].(*Commit)
		if ok {
			sortedEvents[i] = commit
		}
	}
	commitSliceEqual := func(a, b []*Commit) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i].String() != b[i].String() {
				return false
			}
		}
		return true
	}
	for _, e := range sortedEvents[1:] {
		if len(e.parents()) > 1 {
			croak("non-linear history detected, multiple parents at %s", e.idMe())
			return
		}
	}
	for _, e := range sortedEvents[:len(sortedEvents)-1] {
		if len(e.children()) > 1 {
			croak("non-linear history detected, multiple children at %s", e.idMe())
			return
		}
	}
	isChildOf := func(later, earlier *Commit) bool {
		for _, c := range later.parents() {
			if c.getMark() == earlier.getMark() {
				return true
			}
		}
		return false
	}
	for i := 0; i < len(sortedEvents)-1; i++ {
		if !isChildOf(sortedEvents[i+1], sortedEvents[i]) {
			croak("selected commit range not contiguous")
			return
		}
	}
	if commitSliceEqual(events, sortedEvents) {
		croak("commits already in desired order")
		return
	}
	lastEvent := sortedEvents[len(sortedEvents)-1]
	events[0].setParents(sortedEvents[0].parents())
	// replaceParent modifies the list that we're iterating over, so we walk backwards
	children := lastEvent.children()
	for i := len(children) - 1; i >= 0; i-- {
		children[i].(*Commit).replaceParent(lastEvent, events[len(events)-1])
	}
	for i, e := range events[:len(events)-1] {
		events[i+1].setParents([]CommitLike{e})
	}
	fileopSliceEqual := func(a, b []*FileOp) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i].String() != b[i].String() {
				return false
			}
		}
		return true
	}
	// Check if fileops still make sense after re-ordering events.
	for _, c := range events {
		ops := make([]*FileOp, 0)
		for _, op := range c.operations() {
			var path string
			if op.op == opD {
				path = op.Path
			} else if op.op == opR || op.op == opC {
				path = op.Source
			}
			if path != "" && c.visible(path) == nil {
				if !bequiet {
					croak("%s '%c' fileop references non-existent '%s' after re-order", c.idMe(), op.op, path)
				}
				continue
			}
			ops = append(ops, op)
		}
		if !fileopSliceEqual(ops, c.operations()) {
			c.setOperations(ops)
			if !bequiet && len(ops) == 0 {
				if logEnable(logWARN) {
					logit("%s no fileops remain after re-order", c.idMe())
				}
			}
		}
	}
	repo.resort()
}

// Renumber the marks in a repo starting from a specified origin.
func (repo *Repository) renumber(origin int, baton *Baton) {
	markmap := make(map[string]int)
	remark := func(m string, id string) string {
		_, ok := markmap[m]
		if ok {
			return fmt.Sprintf(":%d", markmap[m])
		}
		panic(fmt.Sprintf("unknown mark %s in %s cannot be renumbered!", m, id))
	}
	repo.markseq = 0
	for _, event := range repo.events {
		switch event.(type) {
		case *Blob:
			blob := event.(*Blob)
			if blob.mark == "" {
				continue
			} else if !strings.HasPrefix(blob.mark, ":") {
				panic("field not in mark format")
			} else {
				markmap[blob.mark] = origin + repo.markseq
				repo.markseq++
			}
		case *Commit:
			commit := event.(*Commit)
			if commit.mark == "" {
				continue
			} else if !strings.HasPrefix(commit.mark, ":") {
				panic("field not in mark format")
			} else {
				markmap[commit.mark] = origin + repo.markseq
				repo.markseq++
			}
		}
	}
	var old string
	var newmark string
	for idx, event := range repo.events {
		switch event.(type) {
		case *Blob:
			blob := event.(*Blob)
			old = blob.mark
			if old != "" {
				newmark := remark(old, event.idMe())
				if logEnable(logUNITE) {
					logit("renumbering %s -> %s in blob mark", old, newmark)
				}
				blob.mark = newmark
				repo.events[idx] = blob
			}
		case *Commit:
			commit := event.(*Commit)
			old = commit.mark
			if old != "" {
				newmark := remark(old, event.idMe())
				if logEnable(logUNITE) {
					logit("renumbering %s -> %s in commit mark", old, newmark)
				}
				commit.mark = newmark
				repo.events[idx] = commit
			}
		case *Tag:
			tag := event.(*Tag)
			old = tag.committish
			if old != "" {
				newmark := remark(old, event.idMe())
				if logEnable(logUNITE) {
					logit("renumbering %s -> %s in tag committish", old, newmark)
				}
				tag.committish = newmark
				repo.events[idx] = tag
			}
		case *Reset:
			reset := event.(*Reset)
			old = reset.committish
			if old != "" {
				newmark := remark(old, event.idMe())
				if logEnable(logUNITE) {
					logit("renumbering %s -> %s in reset committish", old, newmark)
				}
				reset.committish = newmark
				repo.events[idx] = reset
			}
		}
	}
	if baton != nil {
		count := len(repo.commits(nil))
		baton.startcounter("renumbering %d of "+fmt.Sprintf("%d", count)+" commits", 0)
	}
	for _, commit := range repo.commits(nil) {
		for i, fileop := range commit.operations() {
			if fileop.op == opM && strings.HasPrefix(fileop.ref, ":") {
				newmark = remark(fileop.ref, "fileop")
				if logEnable(logUNITE) {
					logit(fmt.Sprintf("renumbering %s -> %s in fileop", fileop.ref, newmark))
				}
				commit.fileops[i].ref = newmark
			}
		}
		if baton != nil {
			baton.bumpcounter()
		}
	}
	// Prevent result from having multiple 'done' trailers.
	origLen := len(repo.events)
	var newEvents = make([]Event, 0)
	for _, event := range repo.events {
		passthrough, ok := event.(*Passthrough)
		if ok && passthrough.text == "done\n" {
			continue
		}
		newEvents = append(newEvents, event)
	}
	if len(repo.events) != origLen {
		repo.events = append(repo.events, newPassthrough(repo, "done\n"))
		repo.declareSequenceMutation("")
	}
	// Previous maps won't be valid
	repo.invalidateObjectMap()
	repo.invalidateMarkToIndex()
	if baton != nil {
		baton.endcounter()
	}
}

// Disambiguate branches, tags, and marks using the specified label.
func (repo *Repository) uniquify(color string, persist map[string]string) map[string]string {
	makename := func(oldname string, obj string, fld string, reverse bool) string {
		newname := ""
		if persist == nil {
			// we're not trying to preserve names
			if reverse {
				newname = color + "-" + oldname
			} else {
				newname = oldname + "-" + color
			}
		} else if _, ok := persist[oldname]; !ok {
			// record name as belonging to this repo
			persist[oldname] = color
			return oldname
		} else if persist[oldname] == color {
			// name belongs here, do nothing
			return oldname
		} else {
			// collision - oldname belongs to a different repo
			if reverse {
				newname = color + "-" + oldname
			} else {
				newname = oldname + "-" + color
			}
		}
		if newname != "" {
			if logEnable(logUNITE) {
				logit("moving %s -> %s in %s.%s", oldname, newname, obj, fld)
			}
			if persist != nil {
				persist[newname] = color
			}
			return newname
		}
		return oldname
	}
	makemark := func(oldname string, obj string, fld string) string {
		if oldname == "" {
			return ""
		}
		if !strings.HasPrefix(oldname, ":") {
			panic("field not in mark format")
		}
		newname := oldname + "-" + color
		if logEnable(logUNITE) {
			logit("moving %s -> %s in %s.%s", oldname, newname, obj, fld)
		}
		return newname
	}
	for _, event := range repo.events {
		switch event.(type) {
		case *Blob:
			blob := event.(*Blob)
			blob.setMark(makemark(blob.mark, "blob", "mark"))
		case *Commit:
			commit := event.(*Commit)
			commit.Branch = makename(commit.Branch,
				"commit", "branch", false)
			commit.setMark(makemark(commit.mark, "commit", "mark"))
			for i, fileop := range commit.fileops {
				if fileop.op == opM && strings.HasPrefix(fileop.ref, ":") {
					newname := fileop.ref + "-" + color
					if logEnable(logUNITE) {
						logit("moving %s -> %s in fileop", fileop.ref, newname)
					}
					commit.fileops[i].ref = newname
				}
			}
		case *Reset:
			reset := event.(*Reset)
			reset.ref = makename(reset.ref, "reset", "ref", false)
			reset.committish = makemark(reset.committish, "tag", "committish")
		case *Tag:
			tag := event.(*Tag)
			tag.tagname = makename(tag.tagname, "tag", "name", true)
			tag.committish = makemark(tag.committish, "tag", "committish")
		}
	}
	repo.invalidateObjectMap()
	return persist
}

// Absorb all events from the repository OTHER into SELF.
// Only vcstype, sourcedir, and basedir are not copied here
// Marks and tag/branch names must have been uniquified first,
// otherwise name collisions could occur in the merged repo.
// Note: This does not perform topological merging, new
// branch joins have to be added explicitly.
func (repo *Repository) absorb(other *Repository) {
	repo.preserveSet = repo.preserveSet.Union(other.preserveSet)
	// Strip feature events off the front, they have to stay in front.
	front := len(repo.frontEvents())
	for {
		passthrough, ok := other.events[0].(*Passthrough)
		if ok {
			repo.insertEvent(passthrough, front, "moving passthroughs")
			front++
			other.events = other.events[1:]
		} else {
			break
		}
	}
	// Trailing "done" if any, has to go
	if len(repo.events) > 0 {
		end := repo.events[len(repo.events)-1]
		passthrough, ok := end.(*Passthrough)
		if ok && passthrough.text == "done\n" {
			repo.events = repo.events[:len(repo.events)-1]
		}
	}
	// Merge in the non-feature events and blobs
	for _, event := range other.events {
		event.moveto(repo)
	}
	repo.events = append(repo.events, other.events...)
	repo.declareSequenceMutation("absorb")
	other.events = nil
	other.cleanup()
}

const invalidGraftIndex = -1

// Graft a repo on to this one at a specified point.
// FIXME: Prevent extra done markers (and fix the help to reflect this)
func (repo *Repository) graft(graftRepo *Repository, graftPoint int, options stringSet) error {
	var persist map[string]string
	var anchor *Commit
	var ok bool
	if graftPoint == invalidGraftIndex {
		persist = make(map[string]string)
	} else {
		persist = nil
		where := repo.events[graftPoint]
		anchor, ok = where.(*Commit)
		if !ok {
			return fmt.Errorf("%s in %s is not a commit",
				where.idMe(), repo.name)
		}
	}
	// Errors aren't recoverable after this
	graftRepo.uniquify(graftRepo.name, persist)
	var graftroot *Commit
	if graftPoint != invalidGraftIndex {
		graftroot = graftRepo.earliestCommit()
	}
	repo.absorb(graftRepo)
	if graftPoint != invalidGraftIndex {
		graftroot.addParentByMark(anchor.mark)

	}
	if options.Contains("--prune") {
		// Prepend a deleteall. Roots have nothing upline to preserve.
		delop := newFileOp(repo)
		delop.construct(deleteall)
		graftroot.prependOperation(delop)
	}
	repo.renumber(1, nil)
	// Resolve all callouts
	unresolved := make([]string, 0)
	for _, commit := range repo.commits(nil) {
		for idx, parent := range commit.parents() {
			parentMark := parent.getMark()
			if isCallout(parentMark) {
				attach := repo.named(parentMark)
				if len(attach) == 1 {
					commit.removeParent(parent)
					newparent := repo.events[attach[0]]
					commit.insertParent(idx, newparent.getMark())
				} else {
					unresolved = append(unresolved, parentMark)
				}
			}
		}
	}
	if len(unresolved) > 0 {
		return fmt.Errorf("unresolved callouts: %v", unresolved)
	}
	return nil
}

// Apply a hook to all paths, returning the set of modified paths.
func (repo *Repository) pathWalk(selection orderedIntSet, hook func(string) string) orderedStringSet {
	if hook == nil {
		hook = func(s string) string { return s }
	}
	modified := newOrderedStringSet()
	for _, ei := range selection {
		event := repo.events[ei]
		if commit, ok := event.(*Commit); ok {
			for i, fileop := range commit.operations() {
				newpath := hook(fileop.Path)
				if newpath != fileop.Path {
					modified.Add(newpath)
				}
				commit.fileops[i].Path = newpath
				if fileop.op == opR || fileop.op == opC {
					newpath = hook(fileop.Source)
					if newpath != fileop.Source {
						modified.Add(newpath)
					}
					fileop.Source = newpath
				}
			}
		}
	}
	sort.Strings(modified)
	return modified
}

func (repo *Repository) splitCommit(where int, splitfunc func([]*FileOp) ([]*FileOp, []*FileOp, error)) error {
	event := repo.events[where]
	// Fileop split happens here
	commit, ok := event.(*Commit)
	if !ok {
		return fmt.Errorf("split location %s is not a commit", event.idMe())
	}
	fileops, fileops2, err := splitfunc(commit.operations())
	if err != nil {
		return err
	}
	if len(fileops) == 0 || len(fileops2) == 0 {
		return errors.New("no-op commit split, repo unchanged")
	}
	repo.insertEvent(commit.clone(repo), where+1, "commit split")
	repo.declareSequenceMutation("commit split")
	commit2 := repo.events[where+1].(*Commit)
	// need a new mark
	//assert(commit.mark == commit2.mark)
	commit2.setMark(commit.repo.newmark())
	// Fix up parent/child relationships
	for _, child := range commit.children() {
		child.(*Commit).replaceParent(commit, commit2)
	}
	commit2.setParents([]CommitLike{commit})
	// and then finalize the ops. DO NOT USE setOperations for the
	// first commit, because it would call forget on each fileop that
	// does not stay there, but moves to the second commit.
	commit2.setOperations(fileops2)
	commit.fileops = fileops
	// Avoid duplicates in the legacy-ID map
	if commit2.legacyID != "" {
		commit2.legacyID += ".split"
	}
	return nil
}

func (repo *Repository) splitCommitByIndex(where int, splitpoint int) error {
	if splitpoint < 0 || !repo.events[where].isCommit() || splitpoint > len(repo.events[where].(*Commit).operations())-1 {
		return errors.New("split index out of bounds, or splitting non-commit")
	}
	return repo.splitCommit(where,
		func(ops []*FileOp) ([]*FileOp, []*FileOp, error) {
			return ops[:splitpoint], ops[splitpoint:], nil
		})
}

func (repo *Repository) splitCommitByPrefix(where int, prefix string) error {
	return repo.splitCommit(where,
		func(ops []*FileOp) ([]*FileOp, []*FileOp, error) {
			var without []*FileOp
			var with []*FileOp
			err := fmt.Errorf("couldn't find '%s' in a fileop path", prefix)
			for _, op := range ops {
				// In Python: lambda ops: ([op for op
				// in ops if
				// !strings.HasPrefix(op.Path,
				// prefix)],
				// [op for op in ops if (op.Path || op.Path)
				// and (op.Path || op.Path).startswith(prefix)]))
				// FIXME: Should error out on C or R ops
				if strings.HasPrefix(op.Path, prefix) {
					with = append(with, op)
					err = nil
				} else {
					without = append(without, op)
				}
			}

			return without, with, err
		})
}

// Return blob for the nearest ancestor to COMMIT of the specified PATH.
func (repo *Repository) blobAncestor(commit *Commit, path string) *Blob {
	var ok bool
	ancestor := commit
	for {
		back := ancestor.parents()
		if len(back) == 0 {
			break
		}
		trial := back[0]
		if ancestor, ok = trial.(*Commit); !ok {
			break // could be a callout
		}
		for _, op := range ancestor.operations() {
			if op.Path == path {
				if op.op == opD {
					// Path had no ancestor after
					// last delete
					return nil
				} else if op.op == opR || op.op == opC {
					path = op.Source
				} else if op.op == opM {
					// Buglet: if this path has
					// multiple ops, we'd probably
					// prefer the last to the
					// first.
					return repo.markToEvent(op.ref).(*Blob)
				}
			}
		}
	}
	return nil
}

func (repo *Repository) dumptimes(w io.Writer) {
	total := repo.timings[len(repo.timings)-1].stamp.Sub(repo.timings[0].stamp)
	commitCount := len(repo.commits(nil))
	if repo.legacyCount <= 0 {
		fmt.Fprintf(w, "        commits:\t%d\n", commitCount)
	} else {
		fmt.Fprintf(w, "        commits:\t%d\t(from %d)\n", commitCount, repo.legacyCount)
	}
	totalf := float64(total)
	for i := range repo.timings {
		if i > 0 {
			interval := repo.timings[i].stamp.Sub(repo.timings[i-1].stamp)
			phase := repo.timings[i].label
			intervalf := float64(interval)
			fmt.Fprintf(w, "%15s:\t%2.2f%%\t%v\n",
				phase, (intervalf*100)/totalf, interval)
		}
	}
	fmt.Fprintf(w, "          total:\t%d/sec\t%v\n",
		int(float64(time.Duration(commitCount)*time.Second)/float64(total)),
		total)
}

// Read a repository using fast-import.
func readRepo(source string, options stringSet, preferred *VCS, extractor Extractor, quiet bool, baton *Baton) (*Repository, error) {
	if logEnable(logSHUFFLE) {
		legend := "nil"
		if extractor != nil {
			legend = "non-nil"
		}
		if preferred != nil {
			respond("looking for a %s repo at %s (extractor %s...", preferred.name, source, legend)
		} else {
			respond("reposurgeon: looking for any repo at %s (extractor %s)...", source, legend)
		}
	}
	// Trickier-than-it-looks department:
	// There are three cases here.
	// 1. extractor and preferred both non-nil.  Use the extractor if there's a matching repo here.
	// 2. preferred is non-nil.  Use that type if there's a matching repo here.
	// 3. extractor and preferred both nil. Look for anything we can read.
	var vcs *VCS
	if extractor != nil || preferred != nil {
		if preferred.manages(source) {
			vcs = preferred // if extractor is non-null it gets picked up below
		} else {
			return nil, fmt.Errorf("couldn't find a repo of desiret type %s under %s", preferred.name, abspath(source))
		}
	} else {
		hitcount := 0
		for i, possible := range vcstypes {
			if possible.manages(source) {
				vcs = &vcstypes[i]
				hitcount++
			}
		}
		if hitcount == 0 {
			return nil, fmt.Errorf("couldn't find a repo under %s", abspath(source))
		} else if hitcount > 1 {
			return nil, fmt.Errorf("too many repos (%d) under %s", hitcount, abspath(source))
		}
		// There's only one base match, and vcs is set.  Forward to a matching extractor if need be
		if vcs.exporter == "" {
			for _, possible := range importers {
				if possible.basevcs.manages(source) {
					extractor = possible.engine
				}
			}
			if extractor == nil {
				return nil, fmt.Errorf("couldn't find an exporter matching %s under %s", vcs.name, abspath(source))
			}
		}
	}
	if logEnable(logSHUFFLE) {
		legend := "base"
		if extractor != nil {
			legend = "extractor"
		}
		if logEnable(logSHUFFLE) {
			logit("found %s repository (%s)", vcs.name, legend)
		}
	}
	repo := newRepository("")
	repo.sourcedir = source
	here, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("readRepo is disoriented: %v", err)
	}
	if logEnable(logSHUFFLE) {
		logit("current directory is %q", here)
	}
	chdir := func(directory string, legend string) {
		os.Chdir(directory)
		if logEnable(logSHUFFLE) {
			logit("changing directory to %s: %s", legend, directory)
		}
	}
	defer chdir(here, "original")
	chdir(repo.sourcedir, "repository directory")
	// We found a matching custom extractor
	if extractor != nil {
		repo.stronghint = true
		streamer := newRepoStreamer(extractor, control.flagOptions["progress"])
		repo, err := streamer.extract(repo, vcs)
		return repo, err
	}
	// We found a matching VCS type
	if vcs != nil {
		repo.hint("", vcs.name, true)
		repo.preserveSet = vcs.preserve
		suppressBaton := control.flagOptions["progress"] && repo.exportStyle().Contains("export-progress")
		commandControl := map[string]string{"basename": filepath.Base(repo.sourcedir)}
		mapper := func(sub string) string {
			for k, v := range commandControl {
				from := "${" + k + "}"
				sub = strings.Replace(sub, from, v, -1)
			}
			return sub
		}
		cmd := os.Expand(repo.vcs.exporter, mapper)
		tp, _, err := readFromProcess(cmd)
		if err != nil {
			return nil, err
		}
		repo.fastImport(context.TODO(), tp, options, source, baton)
		tp.Close()
		if suppressBaton {
			control.flagOptions["progress"] = true
		}
		if repo.vcs.authormap != "" && exists(repo.vcs.authormap) {
			if logEnable(logSHOUT) {
				logit("reading author map.")
			}
			fp, err := os.Open(repo.vcs.authormap)
			if err != nil {
				return nil, err
			}
			repo.readAuthorMap(repo.all(), fp)
			fp.Close()
		}
		legacyMap := vcs.subdirectory + "/legacy_map"
		legacyMap = filepath.FromSlash(legacyMap)
		if exists(legacyMap) {
			rfp, err := os.Open(legacyMap)
			if err != nil {
				return nil, err
			}
			repo.readLegacyMap(rfp, baton)
			rfp.Close()
		}
		if vcs.pathlister != "" {
			registered := newOrderedStringSet()
			stdout, cmd, err := readFromProcess(vcs.pathlister)
			if err != nil {
				return nil, err
			}
			// Get the names of all files under version control
			r := bufio.NewReader(stdout)
			for {
				line, err2 := r.ReadString(byte('\n'))
				if err2 == io.EOF {
					if cmd != nil {
						cmd.Wait()
					}
					break
				} else if err2 != nil {
					return nil, fmt.Errorf("while collecting file manifestL %v", err2)
				}
				registered = append(registered, strings.TrimSpace(line))
			}
			stdout.Close()
			// Get the names of all files except those in the
			// repository metadata directory and reposurgeon
			// scratch directories
			var allfiles = newOrderedStringSet()
			err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
				if err != nil {
					croak("path access failure %q: %v", path, err)
					return err
				}
				if info.IsDir() && (info.Name() == vcs.subdirectory || strings.HasPrefix(info.Name(), ".rs")) {
					return filepath.SkipDir
				}
				allfiles = append(allfiles, path)
				return nil
			})
			// Compute the things to preserve
			repo.preserveSet = repo.preserveSet.Union(allfiles.Subtract(registered))
		}
		// kluge: git-specific hook
		if repo.vcs.name == "git" {
			if exists(".git/cvs-revisions") {
				if logEnable(logSHOUT) {
					logit("reading cvs-revisions map.")
				}
				type pathRev struct {
					path string
					rev  string
				}
				pathRevToHash := make(map[pathRev]string)
				// Pass 1: Get git's path/revision to
				// hash mapping
				fp, err := os.Open(".git/cvs-revisions")
				if err != nil {
					return nil, err
				}
				defer fp.Close()
				r := bufio.NewReader(fp)
				for {
					line, err1 := r.ReadString(byte('\n'))
					if err1 == io.EOF {
						break
					} else if err1 != nil {
						return nil, fmt.Errorf("reading cvs-revisions map: %v", err1)
					}

					fields := strings.Fields(line)
					pathrev := pathRev{fields[0], fields[1]}
					hashv := fields[2]
					pathRevToHash[pathrev] = hashv
				}
				// Pass 2: get git's hash to
				// (time,person) mapping
				hashToAction := make(map[string]string)
				stampSet := make(map[string]bool)
				fp2, _, err2 := readFromProcess("git log --all --format='%H %ct %ce'")
				if err2 != nil {
					return nil, err2
				}
				r2 := bufio.NewReader(fp2)
				for {
					line, err2 := r2.ReadString(byte('\n'))
					if err2 == io.EOF {
						break
					} else if err2 != nil {
						return nil, fmt.Errorf("reading cvs-revisions map: %v", err2)
					}

					fields := strings.Fields(line)
					hashv := fields[0]
					ctime := fields[1]
					cperson := fields[2]
					inttime, err3 := strconv.Atoi(ctime)
					if err3 != nil {
						return nil, fmt.Errorf("while reang git hash mapping: %v", err3)
					}
					stamp := rfc3339(time.Unix(int64(inttime), 0))
					stamp += "!" + cperson
					if stampSet[stamp] {
						croak("more than one commit matches %s (%s)",
							stamp, hashv)

						delete(hashToAction, hashv)
					} else {
						hashToAction[hashv] = stamp
						stampSet[stamp] = true
					}
				}
				// Pass 3: build a time+person
				// to commit mapping.
				actionToCommit := make(map[string]*Commit)
				for _, commit := range repo.commits(nil) {
					actionToCommit[commit.committer.actionStamp()] = commit
				}
				// Pass 4: use it to set commit properties
				for pathrev, value := range pathRevToHash {
					if stamp, ok := hashToAction[value]; ok {
						actionToCommit[stamp].legacyID = fmt.Sprintf("CVS:%s:%s", pathrev.path, pathrev.rev)
					}
				}
			}
		}
	}
	return repo, nil
}

// Rebuild a repository from the captured state.
func (repo *Repository) rebuildRepo(target string, options stringSet,
	preferred *VCS, baton *Baton) error {
	if target == "" && repo.sourcedir != "" {
		target = repo.sourcedir
	}
	if target != "" {
		var err error
		target, err = filepath.Abs(target)
		if err != nil {
			return fmt.Errorf("while computing target: %v", err)
		}
	} else {
		return errors.New("no default destination for rebuild")
	}
	vcs := preferred
	if vcs == nil {
		vcs = repo.vcs
	}
	if vcs == nil {
		return errors.New("please prefer a repo type first")
	}
	if vcs.importer == "" {
		return fmt.Errorf("%s repositories supported for read only",
			vcs.name)

	}
	chdir := func(directory string, legend string) {
		os.Chdir(directory)
		if logEnable(logSHUFFLE) {
			logit("changing directory to %s: %s", legend, directory)
		}
	}
	// Create a new empty directory to do the rebuild in
	var staging string
	if !exists(target) {
		staging = target
		err := os.Mkdir(target, userReadWriteSearchMode)
		if err != nil {
			return fmt.Errorf("target directory creation failed: %v", err)
		}
	} else {
		staging = fmt.Sprintf("%s-stage%d", target, os.Getpid())
		if !filepath.IsAbs(target) || !filepath.IsAbs(staging) {
			return fmt.Errorf("internal error: target (%s) and staging paths (%s) should be absolute", target, staging)
		}
		err := os.Mkdir(staging, userReadWriteSearchMode)
		if err != nil {
			return fmt.Errorf("staging directory creation failed: %v", err)
		}
	}
	// Try the rebuild in the empty staging directory
	here, err2 := os.Getwd()
	if err2 != nil {
		return fmt.Errorf("buildRepo is disoriented: %v", err2)
	}
	chdir(staging, "staging")
	defer func() {
		chdir(here, "original")
		if staging != target {
			nuke(staging, "reposurgeon: removing staging directory")
		}
	}()

	if vcs.initializer != "" {
		runProcess(vcs.initializer, "repository initialization")
	}
	params := map[string]string{"basename": filepath.Base(target)}
	mapper := func(sub string) string {
		for k, v := range params {
			from := "${" + k + "}"
			sub = strings.Replace(sub, from, v, -1)
		}
		return sub
	}
	cmd := os.Expand(vcs.importer, mapper)
	tp, cls, err := writeToProcess(cmd)
	if err != nil {
		return err
	}
	repo.fastExport(nil, tp, options, preferred, baton)
	tp.Close()
	cls.Wait()
	if repo.writeLegacy {
		legacyfile := filepath.FromSlash(vcs.subdirectory + "/legacy-map")
		wfp, err := os.OpenFile(legacyfile,
			os.O_WRONLY|os.O_CREATE|os.O_TRUNC, userReadWriteMode)
		if err != nil {
			return fmt.Errorf("legacy-map file %s could not be written: %v",
				legacyfile, err)
		}
		defer wfp.Close()
		err = repo.writeLegacyMap(wfp, baton)
		if err != nil {
			return err
		}
	}
	shouldCheckout := true
	if preferred.name == "git" {
		// Prefer master, but choose another one if master does not exist
		var branch string
		for _, branch = range repo.branchset() {
			if branch == "refs/heads/master" {
				break
			}
		}
		if branch != "" {
			runProcess(fmt.Sprintf("git symbolic-ref HEAD %s", branch), "setting default branch")
		} else {
			// Do not try to checkout a repository with no refs: it is empty
			// and git will show a "fatal" error. Print a warning instead.
			croak("not trying to checkout an empty repository")
			shouldCheckout = false
		}
	}
	if shouldCheckout {
		if vcs.checkout != "" {
			runProcess(vcs.checkout, "repository checkout")
		} else {
			croak("checkout not supported for %s skipping", vcs.name)
		}
	}
	respond("rebuild is complete.")
	ljoin := func(sup string, sub string) string {
		return filepath.FromSlash(sup + "/" + sub)
	}
	chdir(here, "original")
	var savedir string
	// This is how we clear away hooks directories in
	// newly-created repos. May not be strictly necessary.
	if logEnable(logSHUFFLE) {
		logit("Nuking %v from staging %s", vcs.prenuke, staging)
	}
	if vcs.prenuke != nil {
		for _, path := range vcs.prenuke {
			os.RemoveAll(ljoin(staging, path))
		}
	}
	if staging == target {
		// For preservation purposes
		savedir = here
	} else {
		// Rebuild succeeded - make an empty backup directory
		backupcount := 1
		for {
			savedir = target + (fmt.Sprintf(".~%d~", backupcount))
			if exists(savedir) {
				backupcount++
			} else {
				break
			}
		}
		if !filepath.IsAbs(savedir) {
			return fmt.Errorf("internal error, savedir %q should be absolute", savedir)
		}
		os.Mkdir(savedir, userReadWriteSearchMode)

		// This is a critical region.  Ignore all signals
		// until we're done.
		//
		// Move the unmodified repo contents in target to the
		// backup directory.  Then move the staging contents to the
		// target directory.  Finally, restore designated files
		// from backup to target.
		// backup to target.
		entries, err := ioutil.ReadDir(target)
		if err != nil {
			return err
		}
		if logEnable(logSHUFFLE) {
			logit("Target %s to backup%s", target, savedir)
		}
		for _, sub := range entries {
			if logEnable(logSHUFFLE) {
				logit("%s -> %s", ljoin(target, sub.Name()), ljoin(savedir, sub.Name()))
			}
			os.Rename(ljoin(target, sub.Name()),
				ljoin(savedir, sub.Name()))
		}
		respond("repo backed up to %s.", relpath(savedir))
		entries, err = ioutil.ReadDir(staging)
		if err != nil {
			return err
		}
		if logEnable(logSHUFFLE) {
			logit("Copy staging %s to target %s", staging, target)
		}
		for _, sub := range entries {
			if logEnable(logSHUFFLE) {
				logit("%s -> %s", ljoin(staging, sub.Name()), ljoin(target, sub.Name()))
			}
			os.Rename(ljoin(staging, sub.Name()),
				ljoin(target, sub.Name()))
		}
		respond("modified repo moved to %s.", target)
		// Critical region ends
	}
	if len(repo.preserveSet) > 0 {
		preserveMe := repo.preserveSet
		if repo.vcs != nil && repo.vcs.authormap != "" {
			preserveMe = append(preserveMe, repo.vcs.authormap)
		}
		if logEnable(logSHUFFLE) {
			logit("Copy preservation set %v from backup %s to target %s", preserveMe, savedir, target)
		}
		for _, sub := range repo.preserveSet {
			src := ljoin(savedir, sub)
			dst := ljoin(target, sub)
			// Beware of adding a target-noxesistence check here,
			// if you do that the VCS config won't get copied because
			// the newly-created one will block it.
			if exists(src) {
				dstdir := filepath.Dir(dst)
				if !exists(dstdir) {
					os.MkdirAll(dstdir, userReadWriteSearchMode)
				}
				if isdir(src) {
					shutil.CopyTree(src, dst, nil)
				} else {
					shutil.Copy(src, dst, false)
				}
			}
		}
		respond("preserved files restored.")
	} else {
		respond("no preservations.")
	}
	return nil
}

var addressRE = regexp.MustCompile(`([^<@>]+\S)\s+<([^<@>\s]+@[^<@>\s]+)>`)
var wsRE = regexp.MustCompile(`\s+`)

// canonicalizeInlineAddress detects and cleans up an email address in a line,
// then breaks the line around it.
func canonicalizeInlineAddress(line string) (bool, string, string, string) {
	// Massage old-style addresses into newstyle
	line = strings.Replace(line, "(", "<", -1)
	line = strings.Replace(line, ")", ">", -1)
	// And another kind of quirks
	line = strings.Replace(line, "&lt;", "<", -1)
	line = strings.Replace(line, "&gt;", ">", -1)
	// Deal with some address masking that can interfere with next stages
	line = strings.Replace(line, "<at>", "@", -1)
	line = strings.Replace(line, "<dot>", ".", -1)
	// Line must contain an email address. Find it.
	addrStart := strings.LastIndex(line, "<")
	addrEnd := strings.Index(line[addrStart+1:], ">") + addrStart + 1
	if addrStart < 0 || addrEnd <= addrStart {
		return false, "", "", ""
	}
	// Remove all other < and > delimiters to avoid malformed attributions
	// After the address, they can be dropped, but before them might come
	// legit parentheses that were converted above.
	pre := strings.Replace(
		strings.Replace(line[:addrStart], "<", "(", -1),
		">", ")", -1)
	post := strings.Replace(line[addrEnd+1:], ">", "", -1)
	email := line[addrStart+1 : addrEnd]
	// Detect more types of address masking
	email = strings.Replace(email, " at ", "@", -1)
	email = strings.Replace(email, " dot ", ".", -1)
	email = strings.Replace(email, " @ ", "@", -1)
	email = strings.Replace(email, " . ", ".", -1)
	// We require exactly one @ in the address, and none outside
	if strings.Count(email, "@") != 1 ||
		strings.Count(pre, "@")+strings.Count(post, "@") > 0 {
		return false, "", "", ""
	}
	return true, pre, fmt.Sprintf("<%s>", strings.TrimSpace(email)), post
}

func (repo *Repository) processChangelogs(selection orderedIntSet, line string, baton *Baton) (bool, int, int, int, int) {
	cm, cd := 0, 0
	var errLock sync.Mutex
	errlines := make([]string, 0)

	// Machinery for recognizing and skipping dates in
	// ChangeLog attribution lines. To add more date formats,
	// put Go time format specifications in the dateFormats
	// literal. The third literal is the common case. The
	// first two are malformations from the GCC history that
	// might be found elsewhere; they need to be before YYYY-MM-DD
	// to avoid false-matching on it.
	var dateFormats = []string{
		"2006-01-02 15:04 -0700",
		"2006-01-02 15:04",
		"2006-01-02",
		"02-01-2006",
		time.UnixDate,
		time.ANSIC}
	type dateSkipper struct {
		format   string
		fmtCount int
		skipre   *regexp.Regexp
	}
	dateSkippers := make([]dateSkipper, 0)
	for _, format := range dateFormats {
		var skip dateSkipper
		skip.format = format
		skip.fmtCount = len(strings.Fields(format))
		skip.skipre = regexp.MustCompile(strings.Repeat(`\S+\s+`, skip.fmtCount))
		dateSkippers = append(dateSkippers, skip)
	}

	parseChangelogLine := func(line string, commit *Commit, filepath string, pos int) string {
		// Parse an attribution line in a ChangeLog entry, get an email address
		if len(line) <= 10 || unicode.IsSpace(rune(line[0])) {
			return ""
		}
		complain := func() {
			errLock.Lock()
			id := commit.idMe()
			if commit.legacyID != "" {
				id += fmt.Sprintf(" <%s>", commit.legacyID)
			}
			errlines = append(errlines,
				fmt.Sprintf("%s at %s has garbled attribution %q",
					filepath, id, line))
			errLock.Unlock()
		}
		ok, pre, email, post := canonicalizeInlineAddress(line)
		if !ok {
			complain()
			return ""
		}

		// Regenerate cleaned up attribution
		line = pre + email + post
		// Scan for a date - it's not an attribution line without one.
		fields := strings.Fields(line)
		for _, item := range dateSkippers {
			if len(fields) >= item.fmtCount {
				possibleDate := strings.Join(fields[:item.fmtCount], " ")
				_, err := time.Parse(item.format, possibleDate)
				if err != nil {
					continue
				}
				m := item.skipre.FindStringIndex(line)
				if m == nil {
					continue
				}
				addr := strings.TrimSpace(line[m[1]:])
				return wsRE.ReplaceAllLiteralString(addr, " ")
			}
		}
		complain()
		return ""
	}
	parseCoAuthor := func(line string) string {
		// Parse a co-author line in a Changelog
		// A co-author must start with a letter after leading space
		foundSpace := false
		for _, r := range line {
			if unicode.IsSpace(r) {
				foundSpace = true
			} else if foundSpace && unicode.IsLetter(r) {
				break
			} else {
				// Neither a space, nor a letter after spaces
				return ""
			}
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return ""
		}
		// Split the address
		ok, pre, email, post := canonicalizeInlineAddress(line)
		if !ok || post != "" {
			return ""
		}
		// Trim spaces around the name, email is already trimmed
		return fmt.Sprintf("%s %s", strings.TrimSpace(pre), email)
	}

	baton.startProgress("processing changelogs", uint64(len(repo.events)))
	attributions := make([]string, len(selection))
	allCoAuthors := make([][]string, len(selection))
	evts := new(Safecounter) // shared between threads, for progression only
	cc := new(Safecounter)
	cl := new(Safecounter)
	logpattern := "/^ChangeLog$/"
	if line != "" {
		logpattern = line
	}
	if len(line) >= 2 && line[0] != line[len(line)-1] {
		croak("regular expression requires matching start and end delimiters")
		return false, 0, 0, 0, 0
	}
	clRe, err := regexp.Compile(logpattern[1 : len(logpattern)-1])
	if err != nil {
		croak("invalid regular expression for changelog matching: /%s/ (%v)", logpattern, err)
		return false, 0, 0, 0, 0
	}
	isChangeLog := func(filename string) bool {
		return clRe.MatchString(filepath.Base(filename))
	}
	repo.walkEvents(selection, func(eventRank int, event Event) {
		event.setDelFlag(false)
		commit, iscommit := event.(*Commit)
		evts.bump()
		defer baton.percentProgress(uint64(evts.value))
		if !iscommit {
			return
		}
		cc.bump()
		// If a changeset is *all* ChangeLog mods, it is probably either
		// a log rotation or a maintainer fixing a typo. In either case,
		// best not to re-attribute this.
		notChangelog := false
		for _, op := range commit.operations() {
			if op.op != opM || !isChangeLog(op.Path) {
				notChangelog = true
				break
			}
		}
		if !notChangelog {
			return
		}
		foundAttribution := ""
		coAuthors := make(map[string]bool, 0)
		// Let's say an attribution is active when its <author><date> line is
		// newly added, or if there is a new non-whitespace line added in the
		// block just following the <author><date> line.  If there is exactly
		// one active attribution, we will use that for the commit author.
		// Else, skip the commit as the attribution would be ambiguous.  This
		// is the case in merge commits: several changelogs are incorporated.
		for _, op := range commit.operations() {
			if op.op == opM && isChangeLog(op.Path) {
				cl.bump()
				// Figure out where we should look for changes in
				// this blob by comparing it to its nearest ancestor.
				then := make([]string, 0)
				if ob := repo.blobAncestor(commit, op.Path); ob != nil {
					then = strings.Split(string(ob.getContent()), "\n")
				}
				newcontent := repo.markToEvent(op.ref).(*Blob).getContent()
				now := strings.Split(string(newcontent), "\n")
				// Analyze the diff
				differ := difflib.NewMatcherWithJunk(then, now, true, nil)
				comparison := differ.GetOpCodes()
				var lastUnchanged difflib.OpCode
				var lastIsValid bool
				for _, difflines := range comparison {
					if difflines.Tag == 'e' {
						lastUnchanged = difflines
						lastIsValid = true
					} else if difflines.Tag == 'i' || difflines.Tag == 'r' {
						for pos := difflines.J1; pos < difflines.J2; pos++ {
							diffline := now[pos]
							if strings.TrimSpace(diffline) != "" {
								attribution := parseChangelogLine(diffline, commit, op.Path, pos)
								foundAt := 0
								if attribution != "" {
									// we found an active attribution line
									foundAt = pos
									goto attributionFound
								} else if lastIsValid {
									// this is not an attribution line, search for
									// the last one since we are in its block
									for j := lastUnchanged.J2 - 1; j >= lastUnchanged.J1; j-- {
										attribution = parseChangelogLine(now[j], commit, op.Path, j)
										if attribution != "" {
											// this is the active attribution
											// corresponding to the added chunk
											foundAt = j
											goto attributionFound
										}
									}
								}
								continue
							attributionFound:
								if foundAttribution != "" &&
									foundAttribution != attribution {
									// there is more than one active, skip the commit
									return
								}
								foundAttribution = attribution
								lastIsValid = false // it is now irrelevant
								// Now search for co-authors below the attribution
								for i := foundAt + 1; i < len(now); i++ {
									coAuthor := parseCoAuthor(now[i])
									if coAuthor == "" {
										break
									}
									coAuthors[coAuthor] = true
								}
							}
						}
					}
					baton.twirl()
				}
			}
		}
		attributions[eventRank] = foundAttribution
		sorted := make([]string, len(coAuthors))
		k := 0
		for coAuthor := range coAuthors {
			sorted[k] = coAuthor
			k++
		}
		sort.Strings(sorted)
		allCoAuthors[eventRank] = sorted
	})
	baton.endProgress()
	for eventRank, eventID := range selection {
		commit, iscommit := repo.events[eventID].(*Commit)
		attribution := attributions[eventRank]
		if !iscommit || attribution == "" {
			continue
		}
		// Invalid addresses will cause fatal errors if they get into a
		// fast-import stream. Filter out bogons...
		matches := addressRE.FindAllStringSubmatch(strings.TrimSpace(attribution), -1)
		if matches == nil {
			if logEnable(logSHOUT) {
				logit("invalid attribution %q in commit %s <%s>", attribution, commit.mark, commit.legacyID)
			}
			continue
		}
		cm++
		newattr := commit.committer.clone()
		newattr.email = matches[0][2]
		newattr.fullname = matches[0][1]
		newattr.date.setTZ("UTC")
		// This assumes email addresses of contributors are unique.
		// We could get wacky results if two people with different
		// human names but identicall email addresses were run through
		// this code, but that outcome seems wildly unlikely.
		if newattr.fullname == "" {
			for _, mapentry := range repo.authormap {
				if newattr.email == mapentry.email {
					newattr.fullname = mapentry.fullname
					break
				}
			}
		}
		if tz, ok := repo.tzmap[newattr.email]; ok { //&& unicode.IsLetter(rune(tz.String()[0])) {
			newattr.date.timestamp = newattr.date.timestamp.In(tz)
		} else if zone := zoneFromEmail(newattr.email); zone != "" {
			newattr.date.setTZ(zone)
		}
		if val, ok := repo.aliases[ContributorID{fullname: newattr.fullname, email: newattr.email}]; ok {
			newattr.fullname, newattr.email = val.fullname, val.email
		}
		if len(commit.authors) == 0 {
			commit.authors = append(commit.authors, *newattr)
		} else {
			// Required because git sometimes fills in the
			// author field from the committer.
			if commit.authors[len(commit.authors)-1].email == commit.committer.email {
				commit.authors = commit.authors[:len(commit.authors)-1]
			}
			if len(commit.authors) == 0 {
				matched := false
				for _, author := range commit.authors {
					if author.email == newattr.email {
						matched = true
					}
				}
				if !matched {
					commit.authors = append(commit.authors, *newattr)
					cd++
					commit.setDelFlag(true)
				}
			}
		}
		// Now fill-in the co-authors
		if len(allCoAuthors[eventRank]) > 0 {
			message := []string{commit.Comment}
			message = append(message, allCoAuthors[eventRank]...)
			commit.Comment = strings.Join(message, "\nCo-Authored-By: ") + "\n"
		}
	}
	repo.invalidateNamecache()
	sort.Slice(errlines, func(i, j int) bool { return errlines[i] < errlines[j] })
	// Sort is requirs to make message order deterministic
	for _, line := range errlines {
		if logEnable(logSHOUT) {
			logit(line)
		}
	}

	return true, cm, cc.value, cd, cl.value
}

// Filter commit metadata (and possibly blobs) through a specified hook.
func (repo *Repository) dataTraverse(prompt string, selection orderedIntSet, hook func(string, map[string]string) string, attributes orderedStringSet, safety bool, quiet bool, baton *Baton) {
	blobs := false
	nonblobs := false
	for _, ei := range selection {
		event := repo.events[ei]
		switch event.(type) {
		case *Blob:
			blobs = true
		default:
			nonblobs = true
		}
		if blobs && nonblobs {
			break
		}
	}
	// Try to prevent user from shooting self in foot
	if safety && blobs && nonblobs {
		croak("cannot transform blobs and nonblobs in same command")
		return
	}
	// If user is transforming blobs, transform all inlines within the range.
	// This is an expensive step because of the sort; avoid doing it
	// when possible.
	if blobs && repo.inlines > 0 {
		for ei := selection[0]; ei <= selection[len(selection)-1]; ei++ {
			event := repo.events[ei]
			if commit, ok := event.(*Commit); ok {
				for _, fileop := range commit.operations() {
					if len(fileop.inline) > 0 {
						selection = append(selection, ei)
					}
				}
			}
		}
		selection.Sort()
	}
	if !quiet {
		baton.startProgress(prompt, uint64(len(selection)))
	}
	altered := new(Safecounter)
	repo.clrDelFlags()
	repo.walkEvents(selection, func(idx int, event Event) {
		if tag, ok := event.(*Tag); ok {
			if nonblobs {
				oldcomment := tag.Comment
				tag.Comment = hook(tag.Comment, nil)
				anychanged := (oldcomment != tag.Comment)
				oldtagger := tag.tagger.who()
				newtagger := hook(oldtagger, nil)
				if oldtagger != newtagger {
					newtagger += " " + tag.tagger.date.String()
					attrib, err := newAttribution(newtagger)
					if err != nil {
						panic(throw("command", "in data traverse of tag: %v", err))
					}
					tag.tagger = attrib
					anychanged = anychanged || true
				}
				if anychanged {
					altered.bump()
					tag.setDelFlag(true)
				}
			}
		} else if commit, ok := event.(*Commit); ok {
			if nonblobs {
				anychanged := false
				if attributes.Contains("c") {
					oldcomment := commit.Comment
					commit.Comment = hook(commit.Comment, nil)
					if oldcomment != commit.Comment {
						anychanged = true
					}
				}
				if attributes.Contains("C") {
					oldcommitter := commit.committer.who()
					newcommitter := hook(oldcommitter, nil)
					changed := (oldcommitter != newcommitter)
					if changed {
						newcommitter += " " + commit.committer.date.String()
						attrib, err := newAttribution(newcommitter)
						if err != nil {
							panic(throw("command", "in data traverse of commit: %v", err))
						}
						commit.committer = *attrib
						anychanged = true
					}
				}
				if attributes.Contains("a") {
					for i := range commit.authors {
						oldauthor := commit.authors[i].who()
						newauthor := hook(oldauthor, nil)
						if oldauthor != newauthor {
							newauthor += " " + commit.authors[i].date.String()
							attrib, err := newAttribution(newauthor)
							if err != nil {
								panic(throw("command", "in data traverse of commit: %v", err))
							}
							commit.authors[i] = *attrib
							anychanged = true
						}
					}
				}
				if anychanged {
					altered.bump()
					commit.setDelFlag(true)
				}
			}
			if blobs {
				for _, fileop := range commit.operations() {
					if len(fileop.inline) > 0 {
						oldinline := fileop.inline
						fileop.inline = []byte(hook(string(fileop.inline), nil))
						if !bytes.Equal(fileop.inline, oldinline) {
							altered.bump()
						}
					}
				}
			}
		} else if blob, ok := event.(*Blob); ok {
			content := string(blob.getContent())
			modified := hook(content, map[string]string{"%PATHS%": fmt.Sprintf("%v", blob.paths(nil))})
			if content != modified {
				blob.setContent([]byte(modified), noOffset)
				altered.bump()
				blob.setDelFlag(true)
			}
		}
		if !quiet {
			baton.percentProgress(uint64(idx))
		}
	})
	if !quiet {
		baton.endProgress()
	} else {
		respond("%d items modified by %s.", altered.value, strings.ToLower(prompt))
	}
}

// accumulateCommits returns the commits derived from a section set through a hook
func (repo *Repository) accumulateCommits(subarg *fastOrderedIntSet,
	operation func(*Commit) []CommitLike, recurse bool) *fastOrderedIntSet {
	commits := repo.commits(newOrderedIntSet(subarg.Values()...))
	if !recurse {
		result := newFastOrderedIntSet()
		for _, commit := range commits {
			for _, x := range operation(commit) {
				result.Add(repo.eventToIndex(x))
			}
		}
		return result
	}
	result := newFastOrderedIntSet(subarg.Values()...)
	// Populate the queue with selected commits
	var queue []CommitLike
	for _, c := range commits {
		queue = append(queue, c)
	}
	// Breadth-first traversal of the graph
	for len(queue) != 0 {
		popped := queue[0].(*Commit)
		queue = queue[1:]
		for _, commit := range operation(popped) {
			ind := repo.eventToIndex(commit)
			if !result.Contains(ind) {
				result.Add(ind)
				queue = append(queue, commit)
			}
		}
	}
	return result
}

// pathRename performas batch path renames by regular expression
func (repo *Repository) pathRename(selection orderedIntSet, sourceRE *regexp.Regexp, targetPattern string, force bool) {
	actions := make([]pathAction, 0)
	for _, commit := range repo.commits(selection) {
		commit.setDelFlag(false)
		for idx := range commit.fileops {
			for _, attr := range []string{"Path", "Source", "Target"} {
				fileop := commit.fileops[idx]
				if oldpath, ok := getAttr(fileop, attr); ok {
					if ok && oldpath != "" && sourceRE.MatchString(oldpath) {
						newpath := GoReplacer(sourceRE, oldpath, targetPattern)
						if !force && commit.visible(newpath) != nil {
							if logEnable(logWARN) {
								logit("rename of %s at %s failed, %s visible in ancestry", oldpath, commit.idMe(), newpath)
							}
							return
						} else if !force && commit.paths(nil).Contains(newpath) {
							if logEnable(logWARN) {
								logit("rename of %s at %s failed, %s exists there", oldpath, commit.idMe(), newpath)
							}
							return
						} else {
							actions = append(actions, pathAction{fileop, commit, attr, newpath})
							commit.setDelFlag(true)
						}
					}
				}
			}
		}
	}
	// All checks must pass before any renames
	for _, action := range actions {
		setAttr(action.fileop, action.attr, action.newpath)
	}
}

// Delete branches as git does, by forgetting all commits reachable only from
// these branches, then renaming the branch of all commits still reachable to
// ensure the deleted branches no longer appear anywhere
func (repo *Repository) deleteBranch(shouldDelete func(string) bool, baton *Baton) {
	// Select resets & commits to keep
	toKeep := newFastOrderedIntSet()
	wrongBranch := newFastOrderedIntSet()
	for i, ev := range repo.events {
		switch event := ev.(type) {
		case *Reset:
			if !shouldDelete(event.ref) {
				toKeep.Add(i)
				if event.committish != "" {
					idx := repo.markToIndex(event.committish)
					if idx != -1 {
						toKeep.Add(idx)
					}
				}
			}
		case *Commit:
			if shouldDelete(event.Branch) {
				wrongBranch.Add(i)
			} else {
				toKeep.Add(i)
			}
		}
		baton.twirl()
	}
	// Augment to all commits reachable from toKeep
	toKeep = repo.accumulateCommits(toKeep,
		func(c *Commit) []CommitLike { return c.parents() }, true)
	// Select resets to delete & unreachable commits
	deletia := []int{}
	lastKeptIdxWithWrongBranch := -1
	for i, ev := range repo.events {
		switch ev.(type) {
		case *Reset, *Commit:
			if toKeep.Contains(i) {
				if wrongBranch.Contains(i) {
					lastKeptIdxWithWrongBranch = i
				}
			} else {
				deletia = append(deletia, i)
			}
		}
		baton.twirl()
	}
	// Now the last remaining commit with the correct branch has necessarily a
	// child with a branch to keep (or it would be unreachable). It has been
	// found in the previous loop; the event is a Commit since wrongBranch only
	// contains commit indices.
	if lastKeptIdxWithWrongBranch != -1 {
		lastCommit := repo.events[lastKeptIdxWithWrongBranch].(*Commit)
		// Use its first child's Branch for all remaining commits with the
		// wrong branch: it will not move any ref since the first child is
		// later and will modify where its Branch points to.
		// Check all children and take a consistent branch in case the order
		// of these children is not reproducible.
		newBranch := ""
		for _, child := range lastCommit.children() {
			if commit, ok := child.(*Commit); ok && !shouldDelete(commit.Branch) {
				if commit.Branch > newBranch {
					newBranch = commit.Branch
				}
			}
		}
		if newBranch == "" {
			panic("Impossible commit with no good children in deleteBranch")
		}
		for i, ev := range repo.events {
			if toKeep.Contains(i) && wrongBranch.Contains(i) {
				ev.(*Commit).setBranch(newBranch)
			}
			baton.twirl()
		}
	}
	// Actually delete the commits only reachable from wrong branches.
	// --no-preserve-refs is to avoid creating new resets on wrong branches
	repo.delete(orderedIntSet(deletia), orderedStringSet{"--no-preserve-refs"}, baton)
	repo._buildNamecache()
}

// readMessageBox modifies repo metadata by reading/merging in a mailbox stream.
func (repo *Repository) readMessageBox(selection orderedIntSet, input io.ReadCloser,
	create bool, emptyOnly bool) {
	updateList := make([]*MessageBlock, 0)
	r := bufio.NewReader(input)
	if r == nil {
		croak("reader creation failed")
		return
	}
	for {
		msg, err := newMessageBlock(r)
		if err == io.EOF {
			break
		} else if err != nil {
			croak("malformed message block: %v", err)
			return
		}
		updateList = append(updateList, msg)
	}
	// First, a validation pass
	attributionByAuthor := make(map[string]Event)
	attributionByCommitter := make(map[string]Event)
	nameMap := make(map[string]*Tag)
	authorCounts := make(map[string]int)
	committerCounts := make(map[string]int)
	for _, commit := range repo.commits(nil) {
		stamp := commit.actionStamp()
		if found, ok := attributionByAuthor[stamp]; ok && found != commit {
			authorCounts[stamp]++
		}
		attributionByAuthor[stamp] = commit
		stamp = commit.committer.actionStamp()
		if found, ok := attributionByCommitter[stamp]; ok && found != commit {
			committerCounts[stamp]++
		}
		attributionByCommitter[stamp] = commit
	}
	for _, event := range repo.events {
		event.setDelFlag(false)
		if tag, ok := event.(*Tag); ok {
			if tag.tagname != "" {
				nameMap[tag.tagname] = tag
			}
			if tag.tagger != nil {
				stamp := tag.tagger.actionStamp()
				if found, ok := attributionByAuthor[stamp]; ok && found != tag {
					authorCounts[stamp]++
				}
				attributionByAuthor[stamp] = tag
			}
		}
	}
	legacyMap := make(map[string]*Commit)
	for _, commit := range repo.commits(nil) {
		if commit.legacyID != "" {
			legacyMap[commit.legacyID] = commit
		}
	}
	// Special case - event creation
	if create {
		for _, message := range updateList {
			if strings.Contains(message.String(), "Tag-Name") {
				blank := newTag(nil, "", "", nil, "")
				attrib, _ := newAttribution("")
				blank.tagger = attrib
				blank.emailIn(message, true)
				commits := repo.commits(nil)
				blank.remember(repo, commits[len(commits)-1].mark)
				repo.addEvent(blank)
			} else {
				blank := newCommit(repo)
				attrib, _ := newAttribution("")
				blank.committer = *attrib
				blank.emailIn(message, true)
				blank.mark = repo.newmark()
				if blank.Branch == "" {
					// Avoids crapping out on name lookup.
					blank.Branch = "generated-" + blank.mark[1:]
				}
				if selection == nil || len(selection) != 1 {
					repo.addEvent(blank)
				} else {
					commit, ok := repo.events[selection[0]].(CommitLike)
					if ok {
						blank.setParents([]CommitLike{commit})
						repo.insertEvent(blank, selection[0]+1, "event creation from message block")
					}
				}
			}
		}
		repo.declareSequenceMutation("event creation")
		return
	}
	// Normal case - no --create
	events := make([]Event, 0)
	errorCount := 0
	var event Event
	for i, message := range updateList {
		event = nil
		if message.getHeader("Event-Number") != "" {
			eventnum, err := strconv.Atoi(message.getHeader("Event-Number"))
			if err != nil {
				croak("event number garbled in update %d: %v", i+1, err)
				errorCount++
			} else {
				if eventnum < 1 || eventnum > len(repo.events) {
					croak("event number %d out of range in update %d",
						eventnum, i+1)
					errorCount++
				} else {
					event = repo.events[eventnum-1]
				}
			}
		} else if message.getHeader("Legacy-ID") != "" {
			event = legacyMap[message.getHeader("Legacy-ID")]
			if event == nil {
				croak("no commit matches legacy-ID %s",
					message.getHeader("Legacy-ID"))
				errorCount++
			}
		} else if message.getHeader("Event-Mark") != "" {
			event = repo.markToEvent(message.getHeader("Event-Mark"))
			if event == nil {
				croak("no commit matches mark %s",
					message.getHeader("Event-Mark"))
				errorCount++
			}
		} else if message.getHeader("Author") != "" && message.getHeader("Author-Date") != "" {
			blank := newCommit(repo)
			attrib, _ := newAttribution("")
			blank.authors = append(blank.authors, *attrib)
			blank.emailIn(message, false)
			stamp := blank.actionStamp()
			event = attributionByAuthor[stamp]
			if event == nil {
				croak("no commit matches stamp %s", stamp)
				errorCount++
			}
			if authorCounts[stamp] > 1 {
				croak("multiple events (%d) match %s", authorCounts[stamp], stamp)
				errorCount++
			}
		} else if message.getHeader("Committer") != "" && message.getHeader("Committer-Date") != "" {
			blank := newCommit(repo)
			attrib, _ := newAttribution("")
			blank.committer = *attrib
			blank.emailIn(message, false)
			stamp := blank.committer.actionStamp()
			event = attributionByCommitter[stamp]
			if event == nil {
				croak("no commit matches stamp %s", stamp)
				errorCount++
			}
			if committerCounts[stamp] > 1 {
				croak(fmt.Sprintf("multiple events (%d) match %s", committerCounts[stamp], stamp))
				errorCount++
			}
		} else if message.getHeader("Tagger") != "" && message.getHeader("Tagger-Date") != "" {
			blank := newTag(repo, "", "", nil, "")
			attrib, _ := newAttribution("")
			blank.tagger = attrib
			blank.emailIn(message, false)
			stamp := blank.tagger.actionStamp()
			event = attributionByAuthor[stamp]
			if event == nil {
				croak("no tag matches stamp %s", stamp)
				errorCount++
			}
			if authorCounts[stamp] > 1 {
				croak("multiple events match %s", stamp)
				errorCount++
			}
		} else if message.getHeader("Tag-Name") != "" {
			blank := newTag(repo, "", "", nil, "")
			attrib, _ := newAttribution("")
			blank.tagger = attrib
			blank.emailIn(message, false)
			event = nameMap[blank.tagname]
			if event == nil {
				croak("no tag matches name %s", blank.tagname)
				errorCount++
			}
		} else {
			croak("no commit matches update %d:\n%s", i+1, message.String())
			errorCount++
		}
		if event != nil {
			ei := repo.eventToIndex(event)
			if ei == -1 {
				croak("event at update %d can't be found in repository", i+1)
				errorCount++
				return
			} else if _, ok := getAttr(event, "emailIn"); ok {
				croak("event %d cannot be modified", ei+1)
				errorCount++
				return
			}
		}
		// Always append, even nil, to stay in sync with updateList
		events = append(events, event)
	}
	if errorCount > 0 {
		croak("%d errors in metadata updates", errorCount)
		return
	}
	// Now apply the updates
	changers := make([]*MessageBlock, 0)
	for i := range updateList {
		event := events[i]
		update := updateList[i]
		check := strings.TrimSpace(update.getHeader("Check-Text"))
		if check != "" && !strings.HasPrefix(strings.TrimSpace(event.getComment()), check) {
			croak("check text mismatch at %s (input %d of %d), expected %q saw %q, bailing out", event.idMe(), i+1, len(updateList), check, event.getComment())
			return
		}
		if emptyOnly {
			if event.getComment() != update.getPayload() && !emptyComment(event.getComment()) {
				croak("nonempty comment at %s (input %d of %d), bailing out", event.idMe(), i+1, len(updateList))
			}
		}

		switch event.(type) {
		case *Commit:
			commit := event.(*Commit)
			if commit.emailIn(update, false) {
				changers = append(changers, update)
				event.setDelFlag(true)
			}
		case *Tag:
			tag := event.(*Tag)
			if tag.emailIn(update, false) {
				changers = append(changers, update)
				event.setDelFlag(true)
			}
		case *Blob:
			blob := event.(*Blob)
			if blob.emailIn(update, false) {
				changers = append(changers, update)
				event.setDelFlag(true)
			}
		}
	}
	if control.isInteractive() {
		if len(changers) == 0 {
			respond("no events modified by msgin.")
		} else {
			respond("%d events modified by msgin.", len(changers))
		}
	}
}

func (repo *Repository) doGraph(selection orderedIntSet, output io.Writer) {
	fmt.Fprint(output, "digraph {\n")
	for _, ei := range selection {
		event := repo.events[ei]
		if commit, ok := event.(*Commit); ok {
			for _, parent := range commit.parentMarks() {
				if selection.Contains(repo.markToIndex(parent)) {
					fmt.Fprintf(output, "\t%s -> %s;\n",
						parent[1:], commit.mark[1:])
				}
			}
		}
		if tag, ok := event.(*Tag); ok {
			fmt.Fprintf(output, "\t\"%s\" -> \"%s\" [style=dotted];\n",
				tag.tagname, tag.committish[1:])
			fmt.Fprintf(output, "\t{rank=same; \"%s\"; \"%s\"}\n",
				tag.tagname, tag.committish[1:])
		}
	}
	for _, ei := range selection {
		event := repo.events[ei]
		if commit, ok := event.(*Commit); ok {
			firstline, _ := splitRuneFirst(commit.Comment, '\n')
			if len(firstline) > 42 {
				firstline = firstline[:42]
			}
			summary := html.EscapeString(firstline)
			cid := commit.mark
			if commit.legacyID != "" {
				cid = commit.showlegacy() + " &rarr; " + cid
			}
			fmt.Fprintf(output, "\t%s [shape=box,width=5,label=<<table cellspacing=\"0\" border=\"0\" cellborder=\"0\"><tr><td><font color=\"blue\">%s</font></td><td>%s</td></tr></table>>];\n",
				commit.mark[1:], cid, summary)
			newbranch := true
			for _, cchild := range commit.children() {
				if child, ok := cchild.(*Commit); ok && commit.Branch == child.Branch {
					newbranch = false
				}
			}
			if newbranch {
				fmt.Fprintf(output, "\t\"%s\" [shape=oval,width=2];\n", commit.Branch)
				fmt.Fprintf(output, "\t\"%s\" -> \"%s\" [style=dotted];\n", commit.mark[1:], commit.Branch)
			}
		}
		if tag, ok := event.(*Tag); ok {
			firstLine, _ := splitRuneFirst(tag.Comment, '\n')
			if len(firstLine) >= graphCaptionLength {
				firstLine = firstLine[:graphCaptionLength]
			}
			summary := html.EscapeString(firstLine)
			fmt.Fprintf(output, "\t\"%s\" [label=<<table cellspacing=\"0\" border=\"0\" cellborder=\"0\"><tr><td><font color=\"blue\">%s</font></td><td>%s</td></tr></table>>];\n", tag.tagname, tag.tagname, summary)
		}
	}
	fmt.Fprint(output, "}\n")
}

func (repo *Repository) doCoalesce(selection orderedIntSet, timefuzz int, changelog bool, debug bool, baton *Baton) int {
	isChangelog := func(commit *Commit) bool {
		return strings.Contains(commit.Comment, "empty log message") && len(commit.operations()) == 1 && commit.operations()[0].op == opM && strings.HasSuffix(commit.operations()[0].Path, "ChangeLog")
	}
	coalesceMatch := func(cthis *Commit, cnext *Commit) bool {
		croakOnFail := logEnable(logDELETE) || debug
		if cthis.committer.email != cnext.committer.email {
			if croakOnFail {
				croak("committer email mismatch at %s", cnext.idMe())
			}
			return false
		}
		if cthis.committer.date.delta(cnext.committer.date) >= time.Duration(timefuzz)*time.Second {
			if croakOnFail {
				croak("time fuzz exceeded at %s", cnext.idMe())
			}
			return false
		}
		if changelog && !isChangelog(cthis) && isChangelog(cnext) {
			return true
		}
		if cthis.Comment != cnext.Comment {
			if croakOnFail {
				croak("comment mismatch at %s", cnext.idMe())
			}
			return false
		}
		return true
	}
	eligible := make(map[string][]string)
	squashes := make([][]string, 0)
	for _, commit := range repo.commits(selection) {
		trial, ok := eligible[commit.Branch]
		if !ok {
			// No active commit span for this branch - start one
			// with the mark of this commit
			eligible[commit.Branch] = []string{commit.mark}
		} else if coalesceMatch(
			repo.markToEvent(trial[len(trial)-1]).(*Commit),
			commit) {
			// This commit matches the one at the
			// end of its branch span.  Append its
			// mark to the span.
			eligible[commit.Branch] = append(eligible[commit.Branch], commit.mark)
		} else {
			// This commit doesn't match the one
			// at the end of its span.  Coalesce
			// the span and start a new one with
			// this commit.
			if len(eligible[commit.Branch]) > 1 {
				squashes = append(squashes, eligible[commit.Branch])
			}
			eligible[commit.Branch] = []string{commit.mark}
		}
	}
	for _, endspan := range eligible {
		if len(endspan) > 1 {
			squashes = append(squashes, endspan)
		}
	}
	for _, span := range squashes {
		// Prevent lossage when last is a ChangeLog commit
		repo.markToEvent(span[len(span)-1]).(*Commit).Comment = repo.markToEvent(span[0]).(*Commit).Comment
		squashable := make([]int, 0)
		for _, mark := range span[:len(span)-1] {
			squashable = append(squashable, repo.markToIndex(mark))
		}
		repo.squash(squashable, orderedStringSet{}, baton)
	}
	return len(squashes)
}

func (repo *Repository) doIncorporate(tarballs []string, commit *Commit, strip int, firewall bool, after bool, date string) {
	// The extra three slots are for the previous commit,
	// the firewall commit (if any) and the following commit.
	// The slots representing leaduing and following commits
	// could be nil if the insertion is at beginning or end of repo.
	var fw int
	if firewall {
		fw = 1
	}
	segment := make([]*Commit, len(tarballs)+2+fw)

	// Compute the point where we want to start inserting generated commits
	var insertionPoint int
	if after {
		insertionPoint = repo.markToIndex(commit.mark) + 1
		segment[0] = commit
	} else {
		insertionPoint = repo.markToIndex(commit.mark) - 1
		for insertionPoint > 0 {
			prev, ok := repo.events[insertionPoint].(*Commit)
			if ok {
				segment[0] = prev
				break
			} else {
				insertionPoint--
			}
		}
	}

	// Generate tarball commits
	for i, tarpath := range tarballs {
		// Create new commit to carry the new content
		blank := newCommit(repo)
		attr, _ := newAttribution("")
		blank.committer = *attr
		blank.repo = repo
		blank.committer.fullname, blank.committer.email = whoami()
		blank.Branch = commit.Branch
		blank.Comment = fmt.Sprintf("Content from %s\n", tarpath)
		var err error
		blank.committer.date, _ = newDate("1970-01-01T00:00:00Z")

		// Clear the branch
		op := newFileOp(repo)
		op.construct(deleteall)
		blank.appendOperation(op)

		// Incorporate the tarball content
		tarfile, err := os.Open(tarpath)
		if err != nil {
			croak("open or read failed on %s", tarpath)
		}
		defer tarfile.Close()

		if logEnable(logSHUFFLE) {
			logit("extracting %s into %s", tarpath, repo.subdir(""))
		}
		repo.makedir("incorporate")
		headers, err := extractTar(repo.subdir(""), tarfile)
		if err != nil {
			croak("error while extracting tarball %s: %s", tarpath, err.Error())
		}
		// Pre-sorting avoids an indeterminacy bug in tarfile
		// order traversal.
		sort.SliceStable(headers, func(i, j int) bool { return headers[i].Name < headers[j].Name })
		newest := time.Date(1970, 1, 1, 0, 0, 0, 0, time.FixedZone("UTC", 0))
		for _, header := range headers {
			if header.ModTime.After(newest) {
				newest = header.ModTime
			}
			b := newBlob(repo)
			repo.insertEvent(b, insertionPoint, "")
			insertionPoint++
			b.setMark(repo.newmark())
			//b.size = header.size
			b.setBlobfile(filepath.Join(repo.subdir(""), header.Name))
			op := newFileOp(repo)
			fn := path.Join(strings.Split(header.Name, string(os.PathSeparator))[strip:]...)
			mode := 0100644
			if header.Mode&0111 != 0 {
				mode = 0100755
			}
			op.construct(opM, strconv.FormatInt(int64(mode), 8), b.mark, fn)
			blank.appendOperation(op)
		}

		blank.committer.date = Date{timestamp: newest}

		// Splice it into the repository
		blank.mark = repo.newmark()
		repo.insertEvent(blank, insertionPoint, "")
		insertionPoint++

		segment[i+1] = blank

		// We get here if incorporation worked OK.
		if date != "" {
			blank.committer.date, err = newDate(date)
			if err != nil {
				croak("invalid date: %s", date)
				return
			}
		}
	}

	if fw == 1 {
		blank := newCommit(repo)
		attr, _ := newAttribution("")
		blank.committer = *attr
		blank.mark = repo.newmark()
		blank.repo = repo
		blank.committer.fullname, blank.committer.email = whoami()
		blank.Branch = commit.Branch
		blank.Comment = fmt.Sprintf("Firewall commit\n")
		op := newFileOp(repo)
		op.construct(deleteall)
		blank.appendOperation(op)
		repo.insertEvent(blank, insertionPoint, "")
		insertionPoint++
		segment[len(tarballs)+1] = blank
	}

	// Go to next commit, if any, and add it to the segment.
	for insertionPoint < len(repo.events) {
		nxt, ok := repo.events[insertionPoint].(*Commit)
		if ok {
			segment[len(segment)-1] = nxt
			break
		} else {
			insertionPoint++
		}
	}

	// Make parent-child links
	for i := 0; i < len(segment)-1; i++ {
		if segment[i] != nil && segment[i+1] != nil {
			segment[i+1].setParents([]CommitLike{segment[i]})
		}
	}
	repo.declareSequenceMutation("")
	repo.invalidateObjectMap()
}

func (repo *Repository) branchlift(sourcebranch string, pathprefix string, newname string) int {
	var sourceroot *Commit
	var liftroot *Commit
	splitcount := 0
	for _, commit := range repo.commits(nil) {
		commit.setDelFlag(false)
		if commit.Branch == sourcebranch {
			if sourceroot == nil {
				sourceroot = commit
			}
			goodcount := 0
			badcount := 0
			for _, trialpath := range commit.paths(nil) {
				if strings.HasPrefix(trialpath, pathprefix) {
					goodcount++
				} else {
					badcount++
				}
			}
			if goodcount > 0 {
				if badcount == 0 {
					// Simple case - all nonempty Source and Path values have the prefix
					commit.Branch = newname
					for _, op := range commit.operations() {
						if strings.HasPrefix(op.Source, pathprefix) {
							op.Source = op.Source[len(pathprefix):]
						}
						if strings.HasPrefix(op.Path, pathprefix) {
							op.Path = op.Path[len(pathprefix):]
						}
					}
					if liftroot == nil {
						liftroot = commit
					}
				} else {
					// Complex case - commit needs to be split because some
					// paths have the prefix but others don't.
					idx := commit.index()
					err := repo.splitCommitByPrefix(idx, pathprefix)
					if err != nil {
						return -1
					}
					liftFrag := repo.events[idx+1].(*Commit)
					liftFrag.Branch = newname
					liftFrag.setDelFlag(true)
					for _, op := range liftFrag.operations() {
						if strings.HasPrefix(op.Source, pathprefix) {
							op.Source = op.Source[len(pathprefix):]
						}
						if strings.HasPrefix(op.Path, pathprefix) {
							op.Path = op.Path[len(pathprefix):]
						}
					}
					if liftroot == nil {
						liftroot = liftFrag
					}
					splitcount++
				}
			}
		}
	}

	// Now we need to fix up ancestry links.
	var sourceparents []CommitLike
	var liftparents []CommitLike
	if sourceroot.hasParents() {
		sourceparents = sourceroot.parents()
	} else {
		sourceparents = make([]CommitLike, 0)
	}
	if liftroot.hasParents() {
		liftparents = liftroot.parents()
	} else {
		liftparents = make([]CommitLike, 0)
	}
	for _, commit := range repo.commits(nil) {
		if commit.Branch == sourcebranch {
			// Preserve merge links on the source branch.
			if len(commit.parents()) > 1 {
				sourceparents = append(sourceparents, commit.parents()[1:]...)
			}
			commit.setParents(sourceparents)
			sourceparents = []CommitLike{commit}
		} else if commit.Branch == newname {
			commit.setParents(liftparents)
			liftparents = []CommitLike{commit}
		}
	}
	return splitcount
}

/* Topologically reduce the repo */
func (repo *Repository) reduce() {
	interesting := newOrderedStringSet()
	for _, event := range repo.events {
		if tag, ok := event.(*Tag); ok {
			interesting.Add(tag.committish)
		} else if reset, ok := event.(*Reset); ok {
			interesting.Add(reset.ref)
		} else if commit, ok := event.(*Commit); ok {
			if len(commit.children()) != 1 || len(commit.parents()) != 1 {
				interesting.Add(commit.mark)
			} else {
				for _, op := range commit.operations() {
					direct := commit.parents()[0]
					var noAncestor bool
					if _, ok := direct.(*Callout); ok {
						noAncestor = true
					} else if commit, ok := direct.(*Commit); ok {
						noAncestor = commit.ancestorCount(op.Path) == 0
					}
					if op.op != opM || noAncestor {
						interesting.Add(commit.mark)
						break
					}
				}
			}
		}
	}
	neighbors := newOrderedStringSet()
	for _, event := range repo.events {
		if commit, ok := event.(*Commit); ok && interesting.Contains(commit.mark) {
			neighbors = neighbors.Union(newOrderedStringSet(commit.parentMarks()...))
			neighbors = neighbors.Union(newOrderedStringSet(commit.childMarks()...))
		}
	}
	interesting = interesting.Union(neighbors)
	deletia := newOrderedIntSet()
	for i, event := range repo.events {
		if commit, ok := event.(*Commit); ok && !interesting.Contains(commit.mark) {
			deletia.Add(i)
		}
	}
	repo.delete(deletia, nil, control.baton)
}

// A RepositoryList is a repository list with selection and access by name.
type RepositoryList struct {
	repo     *Repository
	repolist []*Repository
	cutIndex int
}

func (rl *RepositoryList) chosen() *Repository {
	return rl.repo
}

func (rl *RepositoryList) choose(repo *Repository) {
	rl.repo = repo
}

func (rl *RepositoryList) unchoose() {
	rl.repo = nil
}

// Return a list of the names of all repositories.
func (rl *RepositoryList) reponames() orderedStringSet {
	var lst = make([]string, len(rl.repolist))
	for i, repo := range rl.repolist {
		lst[i] = repo.name
	}
	return lst
}

// Uniquify a repo name in the repo list.
func (rl *RepositoryList) uniquify(name string) string {
	if strings.HasSuffix(name, ".fi") {
		name = name[:len(name)-3]
	} else if strings.HasSuffix(name, ".svn") {
		name = name[:len(name)-4]
	}
	// repo "foo" is #1
	for seq := 1; ; seq++ {
		var trial string
		if seq == 1 {
			trial = name
		} else {
			trial = name + fmt.Sprintf("%d", seq)
		}
		collision := false
		for _, repo := range rl.repolist {
			if repo.name == trial {
				collision = true
			}
		}
		if !collision {
			return trial
		}
	}
}

// Retrieve a repo by name.
func (rl *RepositoryList) repoByName(name string) *Repository {
	for _, repo := range rl.repolist {
		if repo.name == name {
			return repo
		}
	}
	panic(throw("command", "no repository named %s is loaded.", name))
}

// Remove a repo by name.
func (rl *RepositoryList) removeByName(name string) {
	if rl.repo != nil && rl.repo.name == name {
		rl.unchoose()
	}
	newList := make([]*Repository, 0)
	for _, repo := range rl.repolist {
		if repo.name != name {
			newList = append(newList, repo)
		}
	}
	rl.repolist = newList
}

// Apply a graph-coloring algorithm to see if the repo can be split here.
func (rl *RepositoryList) cutConflict(early *Commit, late *Commit) (bool, int, error) {
	cutIndex := -1
	for i, m := range late.parentMarks() {
		if m == early.mark {
			cutIndex = i
			break
		}
	}
	if cutIndex == -1 {
		err := fmt.Errorf("commit %s is not a parent of commit %s", early.mark, late.mark)
		return false, -1, err
	}
	late.removeParent(early)
	doColor := func(commitlike CommitLike, color colorType) {
		commitlike.setColor(color)
		if commit, ok := commitlike.(*Commit); ok {
			for _, fileop := range commit.operations() {
				if fileop.op == opM && fileop.ref != "inline" {
					blob := rl.repo.markToEvent(fileop.ref)
					//assert isinstance(repo.repo[blob], Blob)
					blob.(*Blob).colors.Add(color)
				}
			}
		}
	}
	doColor(early, colorEARLY)
	doColor(late, colorLATE)
	conflict := false
	keepgoing := true
	for keepgoing && !conflict {
		keepgoing = false
		for _, event := range rl.repo.commits(nil) {
			if event.color != 0 {
				for _, neighbor := range event.parents() {
					if neighbor.getColor() == colorNONE {
						doColor(neighbor, event.color)
						keepgoing = true
						break
					} else if neighbor.getColor() != event.color {
						conflict = true
						break
					}
				}
				for _, neighbor := range event.children() {
					if neighbor.getColor() == colorNONE {
						doColor(neighbor, event.color)
						keepgoing = true
						break
					} else if neighbor.getColor() != event.color {
						conflict = true
						break
					}
				}
			}
		}
	}
	return conflict, cutIndex, nil
}

// Undo a cut operation and clear all colors.
func (repo *Repository) cutClear(early *Commit, late *Commit, cutIndex int) {
	late.insertParent(cutIndex, early.mark)
	for _, event := range repo.events {
		switch event.(type) {
		case *Blob:
			event.(*Blob).colors.Clear()
		case *Commit:
			event.(*Commit).color = colorNONE
		}
	}
}

// Attempt to topologically cut the selected repo.
func (rl *RepositoryList) cut(early *Commit, late *Commit) bool {
	ok, idx, err := rl.cutConflict(early, late)
	if !ok {
		rl.repo.cutClear(early, late, idx)
		return false
	}
	if err != nil {
		croak(err.Error())
	}
	// Repo can be split, so we need to color tags
	for _, event := range rl.repo.events {
		t, ok := event.(*Tag)
		if ok {
			for _, c := range rl.repo.commits(nil) {
				if c.mark == t.committish {
					t.color = c.color
				}
			}
		}
	}
	// Front events go with early segment, they'll be copied to late one.
	for _, event := range rl.repo.frontEvents() {
		if passthrough, ok := event.(*Passthrough); ok {
			passthrough.color = "early"
		}
	}
	//assert all(hasattr(x, "color") || hasattr(x, "colors") || isinstance(x, Reset) for x in rl.repo)
	// Resets are tricky.  One may have both colors.
	// Blobs can have both colors too, through references in
	// commits on both sides of the cut, but we took care
	// of that earlier.
	earlyBranches := newOrderedStringSet()
	lateBranches := newOrderedStringSet()
	for _, commit := range rl.repo.commits(nil) {
		if commit.color == colorNONE {
			croak(fmt.Sprintf("%s is uncolored!", commit.mark))
		} else if commit.color == colorEARLY {
			earlyBranches.Add(commit.Branch)
		} else if commit.color == colorLATE {
			lateBranches.Add(commit.Branch)
		}
	}
	// Now it's time to do the actual partitioning
	earlyPart := newRepository(rl.repo.name + "-early")
	os.Mkdir(earlyPart.subdir(""), userReadWriteSearchMode)
	latePart := newRepository(rl.repo.name + "-late")
	os.Mkdir(latePart.subdir(""), userReadWriteSearchMode)
	for _, event := range rl.repo.events {
		if reset, ok := event.(*Reset); ok {
			if earlyBranches.Contains(reset.ref) {
				earlyPart.addEvent(reset)
			}
			if lateBranches.Contains(reset.ref) {
				latePart.addEvent(reset)
			}
		} else if blob, ok := event.(*Blob); ok {
			if blob.colors.Contains(colorEARLY) {
				earlyPart.addEvent(blob.clone(earlyPart))
			}
			if blob.colors.Contains(colorLATE) {
				latePart.addEvent(blob.clone(latePart))
			}
		} else {
			if passthrough, ok := event.(*Passthrough); ok {
				if passthrough.color == "early" {
					passthrough.moveto(earlyPart)
					earlyPart.addEvent(passthrough)
				} else if passthrough.color == "late" {
					passthrough.moveto(latePart)
					latePart.addEvent(passthrough)
				} else {
					// TODO: Someday, color passthroughs
					// that aren't fronted.
					panic(fmt.Sprintf("coloring algorithm failed on %s", event.idMe()))
				}
			} else if commit, ok := event.(*Commit); ok {
				if commit.color == colorEARLY {
					commit.moveto(earlyPart)
					earlyPart.addEvent(commit)
				} else if commit.color == colorLATE {
					commit.moveto(latePart)
					latePart.addEvent(commit)
				} else {
					panic(fmt.Sprintf("coloring algorithm failed on %s", event.idMe()))
				}
			} else if tag, ok := event.(*Tag); ok {
				if tag.color == colorEARLY {
					tag.moveto(earlyPart)
					earlyPart.addEvent(tag)
				} else if tag.color == colorLATE {
					tag.moveto(latePart)
					latePart.addEvent(tag)
				} else {
					panic(fmt.Sprintf("coloring algorithm failed on %s", event.idMe()))
				}
			}
		}
	}
	// Options and features may need to be copied to the late fragment.
	// It's crucial here that frontEvents() returns a copy, not a reference.
	latePart.events = append(earlyPart.frontEvents(), latePart.events...)
	latePart.declareSequenceMutation("cut operation")
	// Add the split results to the repo list.
	rl.repolist = append(rl.repolist, earlyPart)
	rl.repolist = append(rl.repolist, latePart)
	rl.repo.cleanup()
	rl.removeByName(rl.repo.name)
	return true
}

// Unite multiple repos into a union repo.
func (rl *RepositoryList) unite(factors []*Repository, options stringSet) {
	for _, x := range factors {
		if len(x.commits(nil)) == 0 {
			croak(fmt.Sprintf("empty factor %s", x.name))
			return
		}
	}
	// Forward time order
	sort.Slice(factors, func(i, j int) bool {
		return factors[i].earliest().Before(factors[j].earliest())
	})
	uname := ""
	for _, x := range factors {
		uname += "+" + x.name
	}

	union := newRepository(uname[1:])
	os.Mkdir(union.subdir(""), userReadWriteSearchMode)

	persist := make(map[string]string)
	for _, factor := range factors {
		persist = factor.uniquify(factor.name, persist)
	}

	roots := make([]*Commit, 0)
	for _, x := range factors {
		roots = append(roots, x.earliestCommit())
	}
	// Calculate commits in the first repo that will be
	// parents for subsequent repos.
	commits := factors[0].commits(nil)
	parents := []*Commit{}
	for _, root := range roots[1:] {
		// Get last commit from the first repo that is earlier
		// or the same time as root from the second repo.
		mostRecent := roots[0]
		for _, event := range commits {
			if event.when().After(root.when()) {
				break
			} else {
				mostRecent = event
			}
		}
		parents = append(parents, mostRecent)
	}

	for _, factor := range factors {
		union.absorb(factor)
		rl.removeByName(factor.name)
	}
	// If all the factors have the same repository type,
	// keep it.  Otherwise set no repository type.
	union.vcs = factors[0].vcs
	for _, factor := range factors {
		if factor.vcs != union.vcs {
			union.vcs = nil
		}
	}
	//dumpEvents := func(repo *Repository) []string {
	//	var out []string
	//	for _, commit := range repo.commits(nil) {
	//		out = append(out, commit.getMark())
	//	}
	//	return out
	//}
	// Graft each root to corresponding parent commit.
	for idx, root := range roots[1:] {
		root.addParentByMark(parents[idx].mark)
		// We may not want files from the
		// ancestral stock to persist in the
		// grafted branch unless they have
		// modify ops in the branch root.
		if options.Contains("--prune") {
			fileop := newFileOp(union)
			fileop.construct(deleteall)
			root.setOperations(append([]*FileOp{fileop}, root.operations()...))
			root.canonicalize()
		}
	}
	// Renumber all events
	union.renumber(1, nil)
	// Put the result on the load list
	rl.repolist = append(rl.repolist, union)
	rl.choose(union)
}

// end
