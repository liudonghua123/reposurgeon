// repocutter is a tool for analyzing and diddecting Subversion stream files.
package main

// Copyright by Eric S. Raymond
// SPDX-License-Identifier: BSD-2-Clause

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	term "golang.org/x/term" // For IsTerminal()
)

const linesep = "\n"
const pathsep = "/" // Arrgh - we should derive this from os.PathSeparator

var doc = `repocutter - stream surgery on SVN dump files
general usage: repocutter [-q] [-r SELECTION] SUBCOMMAND

In all commands, the -r (or --range) option limits the selection of revisions
over which an operation will be performed. A selection consists of one or more
comma-separated ranges. A range may consist of an integer revision number or
the special name HEAD for the head revision. Or it may be a colon-separated
pair of integers, or an integer followed by a colon followed by HEAD.

Normally, each subcommand produces a progress spinner on standard error; each
turn means another revision has been filtered. The -q (or --quiet) option
suppresses this.

Type 'repocutter help <subcommand>' for help on a specific subcommand.

Available subcommands and help topics:
   closure
   deselect
   expunge
   log
   obscure
   pathlist
   pathrename
   pop
   propdel
   proprename
   propset
   push
   reduce
   renumber
   replace
   see
   select
   setlog
   sift
   split
   strip
   swap
   swapsvn
   testify
   version
`

// Translated from the 2017-12-13 version of repocutter,
// which began life as 'svncutter' in 2009.  The obsolete
// 'squash' command has been omitted.

var debug int

const debugLOGIC = 1
const debugPARSE = 2

var quiet, docgen bool

var oneliners = map[string]string{
	"closure":    "Compute the transitive closure of a path set",
	"deselect":   "Deselecting revisions",
	"expunge":    "Expunge operations by Node-path header",
	"log":        "Extracting log entries",
	"obscure":    "Obscure pathnames",
	"pathlist":   "List all distinct paths in a stream",
	"pathrename": "Transform path headers with a regexp replace",
	"pop":        "Pop the first segment off each path",
	"propdel":    "Deleting revision properties",
	"proprename": "Renaming revision properties",
	"propset":    "Setting revision properties",
	"push":       "Push a first segment onto each path",
	"reduce":     "Topologically reduce a dump.",
	"renumber":   "Renumber revisions so they're contiguous",
	"replace":    "Regexp replace in blobs",
	"see":        "Report only essential topological information",
	"select":     "Selecting revisions",
	"setlog":     "Mutating log entries",
	"sift":       "Sift for operations by Node-path header",
	"split":      "Split a copy operation into a trunk/branches/tags clique",
	"strip":      "Replace content with unique cookies, preserving structure",
	"swap":       "Swap first two components of pathnames",
	"swapsvn":    "Subversion structure-aware swap",
	"testify":    "Massage a stream file into a neutralized test load",
	"version":    "Report repocutter's version",
}

var helpdict = map[string]string{
	"closure": `closure: usage: repocutter [-q] closure PATH...

The 'closure' subcommand computes the transitive closure of a path set under the
relation 'copies from' - that is, with the smallest set of additional paths such
that every copy-from source is in the set.
`,
	"deselect": `deselect: usage: repocutter [-q] [-r SELECTION] deselect

The 'deselect' subcommand selects a range and permits only revisions NOT in
that range to pass to standard output.
`,
	"expunge": `expunge: usage: repocutter [-r SELECTION ] expunge PATTERN...

Delete all operations with Node-path or Node-copyfrom-path headers matching 
specified Golang regular expressions (opposite of 'sift').  Any revision
left with no Node records after this filtering has its Revision
record is removed as well.
`,
	"log": `log: usage: repocutter [-r SELECTION] log

Generate a log report, same format as the output of svn log on a
repository, to standard output.
`,
	"obscure": `obscure: usage: repocutter [-r SELECTION] obscure

Replace path segments and committer IDs with arbitrary but consistent
names in order to obscure them. The replacement algorithm is tuned to
make the replacements readily distinguishable by eyeball.  This
transform can be restricted by a selection set.
`,
	"pathlist": `pathlist: usage: repocutter [-r SELECTION ] pathlist

List all distinct node-paths in the stream, once each, in the order first
encountered. 
`,
	"pathrename": `pathrename: usage: repocutter [-r SELECTION ] pathrename {FROM TO}+

Modify Node-path headers, Node-copyfrom-path headers, and
svn:mergeinfo properties matching the specified Golang regular
expression FROM; replace with TO.  TO may contain Golang-style
backreferences (${1}, ${2} etc - curly brackets not optional) to
parenthesized portions of FROM. 

Matches are constrained so that each match must be a path segment or a
sequence of path segments; that is, the left end must be either at the
start of path or immediately following a /, and the right end must
precede a / or be at end of string.  With a leading ^ the match is
constrained to be a leading sequence of the pathname; with a trailing
$, a trailing one.

Multiple FROM/TO pairs may be specified and are applied in order.
This transform can be restricted by a selection set.

`,
	"propdel": `propdel: usage: repocutter [-r SELECTION] propdel PROPNAME...

Delete the property PROPNAME. May be restricted by a revision
selection. You may specify multiple properties to be deleted.
`,
	"pop": `pop: usage: repocutter [-r SELECTION ] pop

Pop initial segment off each path. May be useful after a sift command to turn
a dump from a subproject stripped from a dump for a multiple-project repository
into the normal form with trunk/tags/branches at the top level.
This transform can be restricted by a selection set.
`,
	"proprename": `proprename: usage: repocutter [-r SELECTION] proprename OLDNAME->NEWNAME...

Rename the property OLDNAME to NEWNAME. May be restricted by a
revision selection. You may specify multiple properties to be renamed.
`,
	"propset": `propset: usage: repocutter [-r SELECTION] propset PROPNAME=PROPVAL...

Set the property PROPNAME to PROPVAL. May be restricted by a revision
selection. You may specify multiple property settings.
`,
	"push": `push: usage: repocutter [-r SELECTION ] push segment

Push an initial segment onto each path.  Normally used to add a "trunk" prefix
to every path ion a flat repository. This transform can be restricted by a selection set.
`,
	"renumber": `renumber: usage: repocutter renumber

Renumber all revisions, patching Node-copyfrom headers as required.
Any selection option is ignored. Takes no arguments.  The -b option 
can be used to set the base to renumber from, defaulting to 0.
`,
	"reduce": `reduce: usage: repocutter [-r selection] reduce

Strip revisions out of a dump so the only parts left those likely to
be relevant to a conversion problem. This is done by dropping every
node that consists of a change on a file and has no property settings.
`,
	"replace": `replace: usage: repocutter replace /REGEXP/REPLACE/

Perform a regular expression search/replace on blob content. The first
character of the argument (normally /) is treated as the end delimiter 
for the regular-expression and replacement parts. This transform can be 
restricted by a selection set.
`,
	"see": `see: usage: repocutter [-r SELECTION] see

Render a very condensed report on the repository node structure, mainly
useful for examining strange and pathological repositories.  File content
is ignored.  You get one line per repository operation, reporting the
revision, operation type, file path, and the copy source (if any).
Directory paths are distinguished by a trailing slash.  The 'copy'
operation is really an 'add' with a directory source and target;
the display name is changed to make them easier to see. This report
can be restricted by a selection set.
`,
	"select": `select: usage: repocutter [-q] [-r SELECTION] select

The 'select' subcommand selects a range and permits only revisions in
that range to pass to standard output.  A range beginning with 0
includes the dumpfile header.
`,
	"setlog": `setlog: usage: repocutter [-r SELECTION] -logentries=LOGFILE setlog

Replace the log entries in the input dumpfile with the corresponding entries
in the LOGFILE, which should be in the format of an svn log output.
Replacements may be restricted to a specified range.
`,
	"sift": `sift: usage: repocutter [-r SELECTION] sift PATTERN...

Delete all operations with Node-path or Node-copyfrom-path headers *not*
matching specified Golang regular expressions (opposite of 'expunge').
Any revision left with no Node records after this filtering has its Revision record
removed as well. This transform can be restricted by a selection set.
`,
	"split": `split: usage: repocutter split PATH...

Transform every stream operation with Node-path PATH in the path list 
into three operations on PATH/trunk. PATH/branches, and PATH/tags. This
operation assumes if the operation is a copy  that structure exists under
the source directory and also mutates Node-copyfrom headers accordingly. 
This transform can be restricted by a selection set.
`,
	"strip": `strip: usage: repocutter [-r SELECTION] strip PATTERN...

Replace content with unique generated cookies on all node paths
matching the specified regular expressions; if no expressions are
given, match all paths.  Useful when you need to examine a
particularly complex node structure. This transform can be restricted
by a selection set.
`,
	"swap": `swap: usage: repocutter [-r SELECTION] swap [PATTERN]

Swap the top two elements of each pathname in every revision in the
selection set. Useful following a sift operation for straightening out
a common form of multi-project repository.  If a PATTERN argument is given, 
only paths matching the pattern are swapped. This transform can be restricted
by a selection set.
`,
	"swapsvn": `swapsvn: usage: repocutter [-r SELECTION] swapsvn [PATTERN]

Like swap, but is aware of Subversion structure.  Used for transforming
multiproject repositories into a standard layout with trunk, tags, and
branches at the top level.

Fires when the second component of a matching path is "trunk", "branches",
or "tags"; passes through all paths for this is not so unaltered. Swaps
"trunk" and the top-level (project) directory straight up.  For tags
and  branches, the following *two* components are swapped to the top.
thus, "foo/branches/release23" becomes "branches/release23/foo",
putting the project directory beneath the branch.

After the swap, more attempts to recognize spans of deletes, copies
into branch directories, and copies into tag subdirectories that are
parallel in all top-level (project) directories. These are coalesced
into single deletes or copies in the inverted structure.

Accordingly, deletes and copies with three-segment sources and
three-segment targets are  transformed; for tags/ and branches/ paths
the last segment (the subdirectory below the branch name)  is dropped,
while for trunk/ paths the last two segments are dropped leaving only
trunk/.  Following duplicate deletes and copies are skipped. 

This has two minor negative consequences. One is that metadata
belonging to all deletes or copies afrter the first one in a coalesced
span is lost.  The other is that branches and tags local to
individual project directories are promoted to global branches and
tags across the entire transformed repository; no content is lost this
way.

Parallel rename sequences are also coalesced.

If a PATTERN argument is given, only paths matching the pattern are swapped.

This transform can be restricted by a selection set.
`,
	"testify": `testify: usage: repocutter [-r SELECTION] testify

Replace commit timestamps with a monotonically increasing clock tick
starting at the Unix epoch and advancing by 10 seconds per commit.
Replace all attributions with 'fred'.  Discard the repository UUID.
Use this to neutralize procedurally-generated streams so they can be
compared. This transform can be restricted by a selection set.
`,
	"version": `version: usage: version

Report major and minor repocutter version.
`,
}

var narrativeOrder []string = []string{
	"select",
	"deselect",
	"see",
	"renumber",

	"log",
	"setlog",

	"propdel",
	"proprename",
	"propset",

	"expunge",
	"sift",
	"closure",

	"pathlist",
	"pathrename",
	"pop",
	"push",

	"split",
	"swap",
	"swapsvn",

	"replace",
	"strip",
	"obscure",
	"reduce",
	"testify",

	"version",
}

func dumpDocs() {
	re := regexp.MustCompile("([a-z][a-z]*):[^\n]*\n")
	for _, item := range narrativeOrder {
		text := helpdict[item]
		text = re.ReplaceAllString(text, `${1}::`)
		text = strings.Replace(text, "\n\n", "\n+\n", -1)
		os.Stdout.WriteString(text)
		os.Stdout.WriteString("\n")
	}
}

// The svnmerge-integrated property is set by svmerge.py.
// Its semantucs are poorly documented, but we process it
// exactly lile svn:mergeinfo and punt that problem to reposurgeon
// on the "first, doo no harm" principle.
var mergeProperties = []string{"svn:mergeinfo", "svnmerge-integrated"}

var base int
var tag string

// Baton - ship progress indications to stderr
type Baton struct {
	stream *os.File
	count  int
	endmsg string
	time   time.Time
}

// NewBaton - create a new Baton object with specified start and end messages
func NewBaton(prompt string, endmsg string) *Baton {
	baton := Baton{
		stream: os.Stderr,
		endmsg: endmsg,
		time:   time.Now(),
	}
	baton.stream.WriteString(prompt + "...")
	if term.IsTerminal(int(baton.stream.Fd())) {
		baton.stream.WriteString(" \b")
	}
	//baton.stream.Flush()
	return &baton
}

// Twirl - twirl the baton indicating progress
func (baton *Baton) Twirl(ch string) {
	if baton.stream == nil {
		return
	}
	if term.IsTerminal(int(baton.stream.Fd())) {
		if ch != "" {
			baton.stream.WriteString(ch)
		} else {
			baton.stream.Write([]byte{"-/|\\"[baton.count%4]})
			baton.stream.WriteString("\b")
		}
	}
	baton.count++
}

// End - operation is done
func (baton *Baton) End(msg string) {
	if msg == "" {
		msg = baton.endmsg
	}
	fmt.Fprintf(baton.stream, "...(%s) %s.\n", time.Since(baton.time), msg)
}

func croak(msg string, args ...interface{}) {
	legend := "repocutter" + tag + ": croaking, " + msg + "\n"
	fmt.Fprintf(os.Stderr, legend, args...)
	os.Exit(1)
}

func announce(msg string, args ...interface{}) {
	if !quiet {
		content := fmt.Sprintf(msg, args...)
		os.Stderr.WriteString("repocutter: " + content + "\n")
	}
}

// LineBufferedSource - Generic class for line-buffered input with pushback.
type LineBufferedSource struct {
	Linebuffer []byte
	source     io.Reader
	reader     *bufio.Reader
	stream     *os.File
	linenumber int
}

// NewLineBufferedSource - create a new source
func NewLineBufferedSource(source io.Reader) LineBufferedSource {
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<setting up NewLineBufferedSource>\n")
	}
	lbs := LineBufferedSource{
		source: source,
		reader: bufio.NewReader(source),
	}
	fd, ok := lbs.source.(*os.File)
	if ok {
		lbs.stream = fd
	}
	return lbs
}

// Rewind - reset source to its beginning, only works when seekable
func (lbs *LineBufferedSource) Rewind() {
	lbs.reader.Reset(lbs.source)
	if lbs.stream != nil {
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<Rewind>\n")
		}
		lbs.stream.Seek(0, 0)
	}
}

// Readline - line-buffered readline.  Return "" on EOF.
func (lbs *LineBufferedSource) Readline() (line []byte) {
	if len(lbs.Linebuffer) != 0 {
		line = lbs.Linebuffer
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<Readline: popping %q>\n", line)
		}
		lbs.Linebuffer = []byte{}
		return
	}
	line, err := lbs.reader.ReadBytes('\n')
	lbs.linenumber++
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<Readline %d: read %q>\n", lbs.linenumber, line)
	}
	if err == io.EOF {
		return []byte{}
	}
	if err != nil {
		croak("I/O error in Readline of LineBufferedSource")
	}
	return
}

// Straight read from underlying file, no buffering.
func (lbs *LineBufferedSource) Read(rlen int) []byte {
	if len(lbs.Linebuffer) != 0 {
		croak("line buffer unexpectedly nonempty after line %d", lbs.linenumber)
	}
	text := make([]byte, 0, rlen)
	chunk := make([]byte, rlen)
	for {
		n, err := lbs.reader.Read(chunk)
		if err != nil && err != io.EOF {
			croak("I/O error in Read of LineBufferedSource")
		}
		text = append(text, chunk[0:n]...)
		if n == rlen {
			break
		}
		rlen -= n
		chunk = chunk[:rlen]
	}
	lbs.linenumber += strings.Count(string(text), "\n")
	return text
}

// Peek at the next line in the source.
func (lbs *LineBufferedSource) Peek() []byte {
	//assert(lbs.Linebuffer is None)
	nxtline, err := lbs.reader.ReadBytes('\n')
	lbs.linenumber++
	if err != nil && err != io.EOF {
		croak("I/O error in Peek of LineBufferedSource: %s", err)
	}
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<Peek %d: buffer=%q + next=%q>\n",
			lbs.linenumber, lbs.Linebuffer, nxtline)
	}
	lbs.Linebuffer = nxtline
	return lbs.Linebuffer
}

// Flush - get the contents of the line buffer, clearing it.
func (lbs *LineBufferedSource) Flush() []byte {
	//assert(lbs.Linebuffer is not None)
	line := lbs.Linebuffer
	lbs.Linebuffer = []byte{}
	return line
}

// Push a line back to the line buffer.
func (lbs *LineBufferedSource) Push(line []byte) {
	//assert(lbs.linebuffer is None)
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<Push: pushing %q>\n", line)
	}
	lbs.Linebuffer = line
}

// HasLineBuffered - do we have one ready to go?
func (lbs *LineBufferedSource) HasLineBuffered() bool {
	return len(lbs.Linebuffer) != 0
}

// Properties -- represent revision or node properties
type Properties struct {
	properties  map[string]string
	propkeys    []string
	propdelkeys []string
}

// NewProperties - create a new Properties object for a revision or node
func NewProperties(source *DumpfileSource) Properties {
	var props Properties
	newprops := make(map[string]string)
	props.properties = newprops
	for {
		currentline := source.Lbs.Peek()
		if bytes.HasPrefix(currentline, []byte("PROPS-END")) {
			break
		}

		if bytes.HasPrefix(currentline, []byte("D ")) {
			source.Require("D")
			keyhd := string(source.Lbs.Readline())
			key := strings.TrimRight(keyhd, linesep)
			props.propdelkeys = append(props.propdelkeys, key)
			continue
		}
		source.Require("K")
		keyhd := string(source.Lbs.Readline())
		key := strings.TrimRight(keyhd, linesep)
		valhd := source.Require("V")
		vlen, _ := strconv.Atoi(string(bytes.Fields(valhd)[1]))
		value := string(source.Lbs.Read(vlen))
		source.Require(linesep)
		props.properties[key] = value
		props.propkeys = append(props.propkeys, key)
	}
	source.Lbs.Flush()
	return props
}

// Stringer - return a representation of properties that can round-trip
func (props *Properties) Stringer() string {
	var b strings.Builder
	for _, key := range props.propkeys {
		fmt.Fprintf(&b, "K %d%s", len(key), linesep)
		fmt.Fprintf(&b, "%s%s", key, linesep)
		fmt.Fprintf(&b, "V %d%s", len(props.properties[key]), linesep)
		fmt.Fprintf(&b, "%s%s", props.properties[key], linesep)
	}
	for _, key := range props.propdelkeys {
		fmt.Fprintf(&b, "D %d%s", len(key), linesep)
		fmt.Fprintf(&b, "%s%s", key, linesep)
	}
	b.WriteString("PROPS-END\n")
	return b.String()
}

// String - use for visualization, need not round-trip
func (props *Properties) String() string {
	if props == nil || len(props.propkeys) == 0 {
		return ""
	}
	txt := ""
	for _, k := range props.propkeys {
		txt += fmt.Sprintf("%s = %q; ", k, props.properties[k])
	}
	for _, k := range props.propdelkeys {
		txt += fmt.Sprintf("delete %s; ", k)
	}
	return txt[:len(txt)-1]
}

// Contains - does a Properties object contain a specified key?
func (props *Properties) Contains(key string) bool {
	_, ok := props.properties[key]
	return ok
}

// Dumpfile parsing machinery goes here

var revisionLine *regexp.Regexp
var textContentLength *regexp.Regexp
var nodeCopyfrom *regexp.Regexp

func init() {
	revisionLine = regexp.MustCompile("Revision-number: ([0-9])")
	textContentLength = regexp.MustCompile("Text-content-length: ([1-9][0-9]*)")
	nodeCopyfrom = regexp.MustCompile("Node-copyfrom-rev: ([1-9][0-9]*)")
}

// DumpfileSource - this class knows about Subversion dumpfile format.
type DumpfileSource struct {
	Lbs              LineBufferedSource
	Baton            *Baton
	Revision         int
	Index            int
	EmittedRevisions map[string]bool
	DirTracking      map[string]bool
}

// NewDumpfileSource - declare a new dumpfile source object with implied parsing
func NewDumpfileSource(rd io.Reader, baton *Baton) DumpfileSource {
	return DumpfileSource{
		Lbs:              NewLineBufferedSource(rd),
		Baton:            baton,
		Revision:         0,
		EmittedRevisions: make(map[string]bool),
		DirTracking:      make(map[string]bool),
	}
	//runtime.SetFinalizer(&ds, func (s DumpfileSource) {s.Baton.End("")})
}

// SetLength - alter the length field of a specified header
func SetLength(header string, data []byte, val int) []byte {
	re := regexp.MustCompile("(" + header + "-length:) ([0-9]+)")
	return re.ReplaceAll(data, []byte("$1 "+strconv.Itoa(val)))
}

// ReadRevisionHeader - read a revision header, parsing its properties.
func (ds *DumpfileSource) ReadRevisionHeader(PropertyHook func(*Properties)) ([]byte, map[string]string) {
	stash := ds.Require("Revision-number:")
	rev := string(bytes.Fields(stash)[1])
	rval, err := strconv.Atoi(rev)
	if err != nil {
		fmt.Printf("repocutter: invalid revision number %s at line %d\n", rev, ds.Lbs.linenumber)
		os.Exit(1)
	}
	ds.Revision = rval
	if debugline := ds.Optional("Debug-level:"); debugline != nil {
		debug, err = strconv.Atoi(string(bytes.Fields(debugline)[1]))
		if err != nil {
			fmt.Printf("repocutter: invalid debug level %s at line %d\n", rev, ds.Lbs.linenumber)
			os.Exit(1)
		}
	}
	ds.Index = 0
	stash = append(stash, ds.Require("Prop-content-length:")...)
	stash = append(stash, ds.Require("Content-length:")...)
	stash = append(stash, ds.Require(linesep)...)
	props := NewProperties(ds)
	if PropertyHook != nil {
		PropertyHook(&props)
		proplen := len(props.Stringer())
		stash = SetLength("Prop-content", stash, proplen)
		stash = SetLength("Content", stash, proplen)
	}
	stash = append(stash, []byte(props.Stringer())...)
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<after append: %d>\n", ds.Lbs.linenumber)
	}
	for string(ds.Lbs.Peek()) == linesep {
		stash = append(stash, ds.Lbs.Readline()...)
	}
	if ds.Baton != nil {
		ds.Baton.Twirl("")
	}
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<ReadRevisionHeader %d: returns stash=%q>\n",
			ds.Lbs.linenumber, stash)
	}
	return stash, props.properties
}

// Require - read a line, requiring it to have a specified prefix.
func (ds *DumpfileSource) Require(prefix string) []byte {
	line := ds.Lbs.Readline()
	if !strings.HasPrefix(string(line), prefix) {
		croak("required prefix '%s' not seen on %q after line %d (r%v)", prefix, line, ds.Lbs.linenumber, ds.Revision)
	}
	//if debug >= debugPARSE {
	//	fmt.Fprintf(os.Stderr, "<Require %s -> %q>\n", strconv.Quote(prefix), viline)
	//}
	return line
}

// Optional - read a line, reporting if it to have a specified prefix.
func (ds *DumpfileSource) Optional(prefix string) []byte {
	line := ds.Lbs.Readline()
	if strings.HasPrefix(string(line), prefix) {
		return line
	}
	ds.Lbs.Push(line)
	return nil
}

// ReadNode - read a node header and body.
func (ds *DumpfileSource) ReadNode(PropertyHook func(*Properties)) (StreamSection, []byte, []byte) {
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<READ NODE BEGINS>\n")
	}
	header := ds.Require("Node-")
	for {
		line := ds.Lbs.Readline()
		if len(line) == 0 {
			fmt.Fprintf(os.Stderr, "repocutter: unexpected EOF in node header\n")
			os.Exit(1)
		}
		m := nodeCopyfrom.FindSubmatch(line)
		if m != nil {
			r := string(m[1])
			if !ds.EmittedRevisions[r] {
				header = append(header, line...)
				header = append(header, ds.Require("Node-copyfrom-path")...)
				continue
			}
		}
		header = append(header, line...)
		if string(line) == linesep {
			break
		}
	}
	properties := ""
	if bytes.Contains(header, []byte("Prop-content-length")) {
		props := NewProperties(ds)
		if PropertyHook != nil {
			PropertyHook(&props)
		}
		properties = props.Stringer()
	}
	// Using a read() here allows us to handle binary content
	content := []byte{}
	cl := textContentLength.FindSubmatch(header)
	if len(cl) > 1 {
		n, _ := strconv.Atoi(string(cl[1]))
		content = append(content, ds.Lbs.Read(n)...)
	}
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<READ NODE ENDS>\n")
	}
	if PropertyHook != nil {
		header = SetLength("Prop-content", header, len(properties))
		header = SetLength("Content", header, len(properties)+len(content))
	}
	section := StreamSection(header)
	if p := section.payload("Node-kind"); p != nil {
		ds.DirTracking[string(section.payload("Node-path"))] = bytes.Equal(p, []byte("dir"))
	}

	return section, []byte(properties), content
}

// ReadUntilNextRevision - Must only be called from renumber, as it
// doesn't apply the revmap.  On the other hand, it can't be confused
// by content resembling dumpfile headers.
func (ds *DumpfileSource) ReadUntilNextRevision(contentLength int) []byte {
	stash := []byte{}
	for {
		line := ds.Lbs.Readline()
		if len(line) == 0 {
			return stash
		}
		if string(line) == "\n" {
			if contentLength > 0 {
				stash = append(stash, ds.Lbs.Read(contentLength)...)
				contentLength = 0
			}
		} else if strings.HasPrefix(string(line), "Revision-number:") {
			ds.Lbs.Push(line)
			return stash
		}

		stash = append(stash, line...)
		if strings.HasPrefix(string(line), "Content-length:") {
			contentLength, _ = strconv.Atoi(string(bytes.Fields(line)[1]))
		}
	}
}

// ReadUntilNext - accumulate lines until the next matches a specified prefix.
func (ds *DumpfileSource) ReadUntilNext(prefix string, revmap map[int]int) []byte {
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<ReadUntilNext: until %s>\n", prefix)
	}
	stash := []byte{}
	for {
		line := ds.Lbs.Readline()
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<ReadUntilNext: sees %q>\n", line)
		}
		if len(line) == 0 {
			return stash
		}
		if strings.HasPrefix(string(line), prefix) {
			ds.Lbs.Push(line)
			if debug >= debugPARSE {
				fmt.Fprintf(os.Stderr, "<ReadUntilNext pushes: %q>\n", line)
			}
			return stash
		}
		// Hack the revision levels in copy-from headers.
		// We're actually modifying the dumpfile contents
		// (rather than selectively omitting parts of it).
		// Note: this will break on a dumpfile that has dumpfiles
		// in its nodes!
		if revmap != nil && strings.HasPrefix(string(line), "Node-copyfrom-rev:") {
			old := bytes.Fields(line)[1]
			oldi, err := strconv.Atoi(string(old))
			if err != nil {
				newrev := []byte(strconv.Itoa(revmap[oldi]))
				line = bytes.Replace(line, old, newrev, 1)
			}
		}
		stash = append(stash, line...)
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<ReadUntilNext: appends %q>\n", line)
		}

	}
}

func (ds *DumpfileSource) say(text []byte) {
	matches := revisionLine.FindSubmatch(text)
	if len(matches) > 1 {
		ds.EmittedRevisions[string(matches[1])] = true
	}
	os.Stdout.Write(text)
}

// SubversionRange - represent a polyrange of Subversion commit numbers
type SubversionRange struct {
	intervals [][2]int
}

// NewSubversionRange - create a new polyrange object
func NewSubversionRange(txt string) SubversionRange {
	var s SubversionRange
	s.intervals = make([][2]int, 0)
	var upperbound int
	for _, item := range strings.Split(txt, ",") {
		var parts [2]int
		if strings.Contains(item, "-") {
			croak("use ':' for version ranges instead of '-'")
		}

		if strings.Contains(item, ":") {
			fields := strings.Split(item, ":")
			if fields[0] == "HEAD" {
				croak("can't accept HEAD as lower bound of a range.")
			}
			parts[0], _ = strconv.Atoi(fields[0])
			if fields[1] == "HEAD" {
				// Be on safe side - could be a 32-bit machine
				parts[1] = math.MaxInt32
			} else {
				parts[1], _ = strconv.Atoi(fields[1])
			}
		} else {
			parts[0], _ = strconv.Atoi(item)
			parts[1], _ = strconv.Atoi(item)
		}
		if parts[0] >= upperbound {
			upperbound = parts[0]
		} else {
			croak("ill-formed range specification")
		}
		s.intervals = append(s.intervals, parts)
	}
	return s
}

// Contains - does this range contain a specified revision?
func (s *SubversionRange) Contains(rev int) bool {
	for _, interval := range s.intervals {
		if rev >= interval[0] && rev <= interval[1] {
			return true
		}
	}
	return false
}

// Upperbound - what is the uppermost revision in the spec?
func (s *SubversionRange) Upperbound() int {
	return s.intervals[len(s.intervals)-1][1]
}

// Report a filtered portion of content.
func (ds *DumpfileSource) Report(selection SubversionRange,
	nodehook func(header StreamSection, properties []byte, content []byte) []byte,
	prophook func(properties *Properties),
	passthrough bool) {

	/*
	 * passthrough - pass through all node text that the nodehook
	 * has not filtered to nil. When any node in a revision passes
	 * through and its revision header has not already beem passed
	 * through, pass that. Properties are shipped (filtered by
	 * revhook) if their node header or revision header is shipped.
	 * It is exeptional for passthrough to be off; other than in
	 * closure(), pathlist(), log(), and see() it is always on.
	 */

	emit := passthrough && selection.intervals[0][0] == 0
	stash := ds.ReadUntilNextRevision(0)
	if emit {
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<early stash dump: %q>\n", stash)
		}
		os.Stdout.Write(stash)
	}
	if !ds.Lbs.HasLineBuffered() {
		return
	}
	// A hack to only apply the property hook on selected revisions.
	selectedProphook := func(properties *Properties) {
		if prophook != nil && selection.Contains(ds.Revision) {
			prophook(properties)
		}
	}
	var nodecount int
	var line []byte
	for {
		ds.Index = 0
		nodecount = 0
		stash, _ := ds.ReadRevisionHeader(selectedProphook)
		for {
			line = ds.Lbs.Readline()
			if len(line) == 0 {
				return
			}
			if string(line) == "\n" {
				if passthrough && emit {
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<passthrough dump: %q>\n", line)
					}
					os.Stdout.Write(line)
				}
				continue
			}
			if strings.HasPrefix(string(line), "Revision-number:") {
				ds.Lbs.Push(line)
				if len(stash) != 0 && nodecount == 0 {
					if passthrough {
						if debug >= debugPARSE {
							fmt.Fprintf(os.Stderr, "<revision stash dump: %q>\n", stash)
						}
						ds.say(stash)
					}
				}
				break
			}
			if strings.HasPrefix(string(line), "Node-") {
				nodecount++
				if strings.HasPrefix(string(line), "Node-path: ") {
					ds.Index++
				}
				ds.Lbs.Push(line)
				header, properties, content := ds.ReadNode(selectedProphook)
				if debug >= debugPARSE {
					fmt.Fprintf(os.Stderr, "<header: %q>\n", header)
					fmt.Fprintf(os.Stderr, "<properties: %q>\n", properties)
					fmt.Fprintf(os.Stderr, "<content: %q>\n", content)
				}
				var nodetxt []byte
				// The nodehook is only applied on selected revisions;
				// others are passed through unaltered.
				if nodehook != nil && selection.Contains(ds.Revision) {
					nodetxt = nodehook(StreamSection(header), properties, content)
				} else {
					nodetxt = append(nodetxt, header...)
					nodetxt = append(nodetxt, properties...)
					nodetxt = append(nodetxt, content...)
				}
				if debug >= debugPARSE {
					fmt.Fprintf(os.Stderr, "<nodetxt: %q>\n", nodetxt)
				}
				emit = len(nodetxt) > 0
				if emit && len(stash) > 0 {
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<appending to: %q>\n", stash)
					}
					nodetxt = append(stash, nodetxt...)
					stash = []byte{}
				}
				if passthrough && len(nodetxt) > 0 {
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<node dump: %q>\n", nodetxt)
					}
					ds.say(nodetxt)
				}
				continue
			}
			croak("at %d, parse of %q doesn't look right, aborting!", ds.Revision, string(line))
		}
	}
}

// Logentry - parsed form of a Subversion log entry for a revision
type Logentry struct {
	author []byte
	date   []byte
	text   []byte
}

// Logfile represents the state of a logfile
type Logfile struct {
	comments map[int]Logentry
	source   LineBufferedSource
}

// Contains - Does the logfile contain an entry for a specified revision
func (lf *Logfile) Contains(revision int) bool {
	_, ok := lf.comments[revision]
	return ok
}

// captureFromProcess runs a specified command, capturing the output.
func captureFromProcess(command string) []byte {
	if !quiet {
		announce("%s: capturing %s", time.Now(), command)
	}
	cmd := exec.Command("sh", "-c", command)
	content, err := cmd.CombinedOutput()
	if err != nil {
		croak("executing %q: %v", cmd, err)
	}
	//if verbose {
	announce(string(content))
	//}
	return content
}

const delim = "------------------------------------------------------------------------"

// NewLogfile - initialize a new logfile object from an input source
func NewLogfile(readable io.Reader, restrict *SubversionRange) *Logfile {
	lf := Logfile{
		comments: make(map[int]Logentry),
		source:   NewLineBufferedSource(readable),
	}
	type LogState int
	const (
		awaitingHeader LogState = iota
		inLogEntry
	)
	state := awaitingHeader
	author := []byte{}
	date := []byte{}
	logentry := []byte{}
	lineno := 0
	rev := -1
	re := regexp.MustCompile("^r[0-9]+")
	var line []byte
	for {
		lineno++
		line = lf.source.Readline()
		if state == inLogEntry {
			if len(line) == 0 || bytes.HasPrefix(line, []byte(delim)) {
				if rev > -1 {
					logentry = bytes.TrimSpace(logentry)
					if restrict == nil || restrict.Contains(rev) {
						lf.comments[rev] = Logentry{author, date, logentry}
					}
					rev = -1
					logentry = []byte{}
				}
				if len(line) == 0 {
					break
				}
				state = awaitingHeader
			} else {
				logentry = append(logentry, line...)
			}
		}
		if state == awaitingHeader {
			if len(line) == 0 {
				break
			}
			if bytes.HasPrefix(line, []byte("-----------")) {
				continue
			}
			if !re.Match(line) {
				fmt.Fprintf(os.Stderr, "line %d: repocutter did not see a comment header where one was expected\n", lineno)
				os.Exit(1)
			}
			fields := bytes.Split(line, []byte("|"))
			revstr := bytes.TrimSpace(fields[0])
			author = bytes.TrimSpace(fields[1])
			date = bytes.TrimSpace(fields[2])
			//lc := bytes.TrimSpace(fields[3])
			revstr = revstr[1:] // strip off leading 'r'
			rev, _ = strconv.Atoi(string(revstr))
			state = inLogEntry
		}
	}
	return &lf
}

// StreamSection is a section of a dump stream interpreted as an RFC-2822-like header
type StreamSection []byte

// Extract content of a specified header field, nil if it doesn't exist
func (ss StreamSection) payload(hd string) []byte {
	offs := bytes.Index(ss, []byte(hd+": "))
	if offs == -1 {
		return nil
	}
	offs += len(hd) + 2
	end := bytes.Index(ss[offs:], []byte("\n"))
	return ss[offs : offs+end]
}

// Mutate a specified header through a hook
func (ss *StreamSection) replaceHook(htype string, hook func([]byte) []byte) (StreamSection, []byte, []byte) {
	header := []byte(*ss)
	offs := bytes.Index(header, []byte(htype))
	if offs > -1 {
		offs += len(htype)
		endoffs := offs + bytes.Index(header[offs:], []byte("\n"))
		before := header[:offs]
		pathline := header[offs:endoffs]
		after := make([]byte, len(header)-endoffs)
		copy(after, header[endoffs:])
		newpathline := hook(pathline)
		header = before
		header = append(header, newpathline...)
		header = append(header, after...)
		return StreamSection(header), newpathline, pathline
	}
	return StreamSection(header), nil, nil
}

// Find the index of the content of a specified field
func (ss StreamSection) index(field string) int {
	return bytes.Index([]byte(ss), []byte(field))
}

// Is this a directory node?
func (ss StreamSection) isDir(context DumpfileSource) bool {
	// Subversion sometimes omits the type field on directory operations.
	// This mwans we need to look back at the type of the directory's last
	// add or change operation.
	if ss.index("Node-kind") == -1 {
		return context.DirTracking[string(ss.payload("Node-path"))]
	}
	return bytes.Equal(ss.payload("Node-kind"), []byte("dir"))
}

// SetLength - alter the length field of a specified header
func (ss StreamSection) setLength(header string, val int) []byte {
	re := regexp.MustCompile("(" + header + "-length:) ([0-9]+)")
	return StreamSection(re.ReplaceAll([]byte(ss), []byte("$1 "+strconv.Itoa(val))))
}

// stripChecksums - remove checksums from a blob header
func (ss StreamSection) stripChecksums() StreamSection {
	header := []byte(ss)
	r1 := regexp.MustCompile("Text-content-md5:.*\n")
	header = r1.ReplaceAll(header, []byte{})
	r2 := regexp.MustCompile("Text-content-sha1:.*\n")
	header = r2.ReplaceAll(header, []byte{})
	r3 := regexp.MustCompile("Text-copy-source-md5:.*\n")
	header = r3.ReplaceAll(header, []byte{})
	r4 := regexp.MustCompile("Text-copy-source-sha1:.*\n")
	header = r4.ReplaceAll(header, []byte{})
	return StreamSection(header)
}

// Subcommand implementations begin here

func doSelect(source DumpfileSource, selection SubversionRange, invert bool) {
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<entering select>")
	}
	emit := (selection.Contains(0) != invert)
	for {
		stash := source.ReadUntilNext("Revision-number:", nil)
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<stash: %q>\n", stash)
		}
		if emit {
			os.Stdout.Write(stash)
		}
		if !source.Lbs.HasLineBuffered() {
			return
		}
		fields := bytes.Fields(source.Lbs.Linebuffer)
		// Error already checked during source parsing
		revision, _ := strconv.Atoi(string(fields[1]))
		emit = (selection.Contains(revision) != invert)
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<%d:%t>\n", revision, emit)
		}
		if emit {
			os.Stdout.Write(source.Lbs.Flush())
		}
		if !invert && revision > selection.Upperbound() {
			return
		}
		source.Lbs.Flush()
	}
}

func closure(source DumpfileSource, selection SubversionRange, paths []string) {
	copiesFrom := make(map[string][]string)
	gather := func(header StreamSection, properties []byte, _ []byte) []byte {
		nodepath := string(header.payload("Node-path"))
		copysource := header.payload("Node-copyfrom-path")
		if copysource != nil {
			copiesFrom[nodepath] = append(copiesFrom[nodepath], string(copysource))
		}
		return nil
	}
	source.Report(selection, gather, nil, false)
	s := newStringSet(paths...)
	for {
		count := s.Len()
		for target := range s.store {
			for _, source := range copiesFrom[target] {
				s.Add(source)
			}
		}
		if count == s.Len() {
			break
		}
	}
	for _, path := range s.toOrderedStringSet() {
		fmt.Println(path)
	}
}

// Select a portion of the dump file defined by a revision selection.
func deselect(source DumpfileSource, selection SubversionRange) {
	doSelect(source, selection, true)
}

// Strip out ops defined by a revision selection and a path regexp.
func expunge(source DumpfileSource, selection SubversionRange, patterns []string) {
	regexps := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		regexps[i] = regexp.MustCompile(pattern)
	}
	expungehook := func(header StreamSection, properties []byte, content []byte) []byte {
		matched := false
		for _, hd := range []string{"Node-path", "Node-copyfrom-path"} {
			nodepath := header.payload(hd)
			if nodepath != nil {
				for _, r := range regexps {
					if r.Match(nodepath) {
						matched = true
						break
					}
				}
			}
		}
		if !matched {
			all := make([]byte, 0)
			all = append(all, []byte(header)...)
			all = append(all, properties...)
			all = append(all, content...)
			return all
		}
		return []byte{}
	}
	source.Report(selection, expungehook, nil, true)
}

func dumpall(header StreamSection, properties []byte, content []byte) []byte {
	// Bad idea - it looks like a way to filter out directory-change
	// operatiuons that only hack properties, but it a;so catches directory
	// add and delete operations.
	//if bytes.Equal(properties, []byte("PROPS-END\n")) && len(content) == 0 {
	//	return nil
	//}
	all := make([]byte, 0)
	all = append(all, []byte(header)...)
	all = append(all, properties...)
	all = append(all, content...)
	return all
}

// Hack pathnames to obscure them.
func obscure(seq NameSequence, source DumpfileSource, selection SubversionRange) {
	pathMutator := func(s []byte) []byte {
		parts := strings.Split(filepath.ToSlash(string(s)), "/")
		for i := range parts {
			if parts[i] != "trunk" && parts[i] != "tags" && parts[i] != "branches" && parts[i] != "" {
				parts[i] = seq.obscureString(parts[i])
			}
		}
		return []byte(filepath.FromSlash(strings.Join(parts, "/")))
	}

	nameMutator := func(s string) string {
		return strings.ToLower(seq.obscureString(s))
	}

	min := func(a, b int) int {
		if a < b {
			return a
		}
		return b
	}

	contentMutator := func(s []byte) []byte {
		// Won't bijectively map link target names longer or
		// shorter than the generated fancyname.  The problem
		// here is that we can't change the length of this
		// content - no way to patch the length headers. Note:
		// ideally we'd also remove the content hashes, they
		// become invalid after this transformation.
		if bytes.HasPrefix(s, []byte("link ")) {
			t := pathMutator(s[5:])
			c := min(len(s)-5, len(t))
			for i := 0; i < c; i++ {
				s[5+i] = t[i]
			}
		}
		return s
	}

	mutatePaths(source, selection, pathMutator, nameMutator, contentMutator)
}

// Pop the top segment off each pathname in an input dump
func pop(source DumpfileSource, selection SubversionRange) {
	popSegment := func(ins string) string {
		if strings.Contains(ins, "/") {
			return ins[strings.Index(ins, "/")+1:]
		}
		return ""
	}
	revhook := func(props *Properties) {
		for _, mergeproperty := range mergeProperties {
			if _, present := props.properties[mergeproperty]; present {
				oldval := props.properties["svn:mergeinfo"]
				rooted := false
				if oldval[0] == os.PathSeparator {
					rooted = true
					oldval = oldval[1:]
				}
				newval := popSegment(oldval)
				if rooted {
					newval = "/" + newval
				}
				props.properties[mergeproperty] = newval
			}
		}
	}
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		for _, htype := range []string{"Node-path: ", "Node-copyfrom-path: "} {
			header, _, _ = header.replaceHook(htype, func(in []byte) []byte {
				return []byte(popSegment(string(in)))
			})
		}
		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, nodehook, revhook, true)
}

// Push a prefix segment onto each pathname in an input dump
func push(source DumpfileSource, selection SubversionRange, prefix string) {
	revhook := func(props *Properties) {
		for _, mergeproperty := range mergeProperties {
			if _, present := props.properties[mergeproperty]; present {
				oldval := props.properties["svn:mergeinfo"]
				rooted := false
				if oldval[0] == os.PathSeparator {
					rooted = true
					if len(oldval) > 0 {
						oldval = oldval[1:]
					}
				}
				newval := prefix + "/" + oldval
				if rooted {
					newval = "/" + newval
				}
				props.properties[mergeproperty] = newval
			}
		}
	}
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		for _, htype := range []string{"Node-path: ", "Node-copyfrom-path: "} {
			header, _, _ = header.replaceHook(htype, func(in []byte) []byte {
				return []byte(prefix + "/" + string(in))
			})
		}
		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, nodehook, revhook, true)
}

// propdel - Delete properties
func propdel(source DumpfileSource, propnames []string, selection SubversionRange) {
	revhook := func(props *Properties) {
		for _, propname := range propnames {
			delete(props.properties, propname)
			for delindex, item := range props.propkeys {
				if item == propname {
					props.propkeys = append(props.propkeys[:delindex], props.propkeys[delindex+1:]...)
					break
				}
			}
			for delindex, item := range props.propdelkeys {
				if item == propname {
					props.propdelkeys = append(props.propdelkeys[:delindex], props.propdelkeys[delindex+1:]...)
					break
				}
			}
		}
	}
	source.Report(selection, dumpall, revhook, true)
}

// Set properties.
func propset(source DumpfileSource, propnames []string, selection SubversionRange) {
	revhook := func(props *Properties) {
		for _, propname := range propnames {
			fields := strings.Split(propname, "=")
			if _, present := props.properties[fields[0]]; !present {
				props.propkeys = append(props.propkeys, fields[0])
			}
			props.properties[fields[0]] = fields[1]
		}
	}
	source.Report(selection, dumpall, revhook, true)
}

// Rename properties.
func proprename(source DumpfileSource, propnames []string, selection SubversionRange) {
	revhook := func(props *Properties) {
		for _, propname := range propnames {
			fields := strings.Split(propname, "->")
			if _, present := props.properties[fields[0]]; present {
				props.properties[fields[1]] = props.properties[fields[0]]
				props.properties[fields[0]] = ""
				for i, item := range props.propkeys {
					if item == fields[0] {
						props.propkeys[i] = fields[1]
					}
				}
				for i, item := range props.propdelkeys {
					if item == fields[0] {
						props.propdelkeys[i] = fields[1]
					}
				}
			}
		}
	}
	source.Report(selection, dumpall, revhook, true)
}

func getAuthor(props map[string]string) string {
	if author, ok := props["svn:author"]; ok {
		return author
	}
	return "(no author)"
}

// SVNTimeParse - parse a date in the Subversion variant of RFC3339 format
func SVNTimeParse(rdate string) time.Time {
	// An example date in SVN format is '2011-11-30T16:40:02.180831Z'
	date, ok := time.Parse(time.RFC3339Nano, rdate)
	if ok != nil {
		fmt.Fprintf(os.Stderr, "ill-formed date '%s': %v\n", rdate, ok)
		os.Exit(1)
	}
	return date
}

// Extract log entries
func log(source DumpfileSource, selection SubversionRange) {
	prophook := func(prop *Properties) {
		props := prop.properties
		logentry := props["svn:log"]
		// This test implicitly excludes r0 metadata from being dumped.
		// It is not certain this is the right thing.
		if logentry == "" {
			return
		}
		os.Stdout.Write([]byte(delim + "\n"))
		author := getAuthor(props)
		date := SVNTimeParse(props["svn:date"])
		drep := date.Format("2006-01-02 15:04:05 +0000 (Mon, 02 Jan 2006)")
		fmt.Printf("r%d | %s | %s | %d lines\n",
			source.Revision,
			author,
			drep,
			strings.Count(logentry, "\n"))
		os.Stdout.WriteString("\n" + logentry + "\n")
	}
	source.Report(selection, nil, prophook, false)
}

// Hack paths by applying a specified transformation.
func mutatePaths(source DumpfileSource, selection SubversionRange, pathMutator func([]byte) []byte, nameMutator func(string) string, contentMutator func([]byte) []byte) {
	revhook := func(props *Properties) {
		for _, mergeproperty := range mergeProperties {
			if _, present := props.properties[mergeproperty]; present {
				mergeinfo := string(props.properties[mergeproperty])
				var buffer bytes.Buffer
				if len(mergeinfo) != 0 {
					for _, line := range strings.Split(strings.TrimSuffix(mergeinfo, "\n"), "\n") {
						if strings.Contains(line, ":") {
							lastidx := strings.LastIndex(line, ":")
							path, revrange := line[:lastidx], line[lastidx+1:]
							if path[0] == os.PathSeparator {
								buffer.WriteByte(byte(os.PathSeparator))
								path = path[1:]
							}
							buffer.Write(pathMutator([]byte(path)))
							buffer.WriteString(":")
							buffer.WriteString(revrange)
						} else {
							buffer.WriteString(line)
						}
						buffer.WriteString("\n")
					}
				}
				props.properties[mergeproperty] = buffer.String()
			}
		}
		if userid, present := props.properties["svn:author"]; present && nameMutator != nil {
			props.properties["svn:author"] = nameMutator(userid)
		}
	}
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		for _, htype := range []string{"Node-path: ", "Node-copyfrom-path: "} {
			header, _, _ = header.replaceHook(htype, pathMutator)
		}
		if contentMutator != nil {
			content = contentMutator(content)
		}
		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, nodehook, revhook, true)
}

func pathlist(source DumpfileSource, selection SubversionRange) {
	pathList := newOrderedStringSet()
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		pathList.Add(string(header.payload("Node-path")))
		return nil
	}
	source.Report(selection, nodehook, nil, false)
	for _, item := range pathList.Iterate() {
		os.Stdout.WriteString(item + "\n")
	}
}

// Hack paths by applying regexp transformations on segment sequences.
func pathrename(source DumpfileSource, selection SubversionRange, patterns []string) {
	if len(patterns)%2 == 1 {
		croak("pathrename can't have odd number of arguments")
	}
	type transform struct {
		re *regexp.Regexp
		to []byte
	}
	ops := make([]transform, 0)
	for i := 0; i < len(patterns)/2; i++ {
		if patterns[i*2][0] == '^' && patterns[i*2][len(patterns[i*2])-1] == '$' {
			ops = append(ops, transform{regexp.MustCompile(patterns[i*2]),
				[]byte(patterns[i*2+1])})
		} else if patterns[i*2][0] == '^' {
			ops = append(ops, transform{regexp.MustCompile(patterns[i*2] + "(?P<end>/|$)"),
				append([]byte(patterns[i*2+1]), []byte("${end}")...)})
		} else if patterns[i*2][0] == '$' {
			ops = append(ops, transform{regexp.MustCompile("(?P<start>^|/)" + patterns[i*2]),
				append([]byte("${start}"), []byte(patterns[i*2+1])...)})
		} else {
			ops = append(ops, transform{regexp.MustCompile("(?P<start>^|/)" + patterns[i*2] + "(?P<end>/|$)"),
				append([]byte("${start}"), append([]byte(patterns[i*2+1]), []byte("${end}")...)...)})
		}
	}
	mutator := func(s []byte) []byte {
		for _, op := range ops {
			s = op.re.ReplaceAll(s, op.to)
		}
		return s
	}

	mutatePaths(source, selection, mutator, nil, nil)
}

// Topologically reduce a dump, removing plain file modifications.
func reduce(source DumpfileSource, selection SubversionRange) {
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		if string(StreamSection(header).payload("Node-kind")) == "file" && string(StreamSection(header).payload("Node-action")) == "change" && len(properties) == 0 {
			return nil
		}
		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, nodehook, nil, true)
}

var renumbering map[int]int

func renumberBack(n int) int {
	v, ok := renumbering[n]
	if ok {
		return v
	}
	m := 0
	for r := range renumbering {
		if r <= n && r > m {
			m = r
		}
	}
	return renumbering[m]
}

func renumberMergeInfo(lines []byte, renumbering map[int]int) []byte {
	modifiedLines := make([]byte, 0)
	for _, line := range bytes.Split(lines, []byte("\n")) {
		line = append(line, '\n')
		out := make([]byte, 0)
		fields := bytes.Split(line, []byte(":"))

		if len(fields) == 1 {
			modifiedLines = append(modifiedLines, fields[0]...)
			continue
		}
		fields[0] = append(fields[0], []byte(":")...)
		digits := make([]byte, 0)
		for _, c := range fields[1] {
			if bytes.ContainsAny([]byte{c}, "0123456789") {
				digits = append(digits, c)
			} else {
				if len(digits) > 0 {
					v, _ := strconv.Atoi(string(digits))
					d := fmt.Sprintf("%d", renumberBack(v))
					out = append(out, []byte(d)...)
					digits = make([]byte, 0)
				}
				out = append(out, c)
			}
		}

		modifiedLines = append(modifiedLines, append(fields[0], out...)...)
	}

	return modifiedLines
}

// Renumber all revisions.
func renumber(source DumpfileSource) {
	renumbering = make(map[int]int)
	counter := base
	var p []byte
	type HeaderState int
	const (
		AwaitingHeader HeaderState = iota
		InHeader
		InProps
		InText
	)

	type TextLengthState int
	const (
		awaitingTextLength TextLengthState = iota
	)

	type RenumberState int
	const (
		awaitingRevisionNumber RenumberState = iota
		awaitingContentLength
		awaitingMergeInfoKey
	)

	type PropParserState int
	const (
		awaitingNext PropParserState = iota
		awaitingPropDelete
		awaitingKeyValue
		awaitingValueLength
		awaitingMergeInfoValueLength
		readingValue
		readingMergeInfo
		propsEnd
	)

	var propParserState = awaitingNext

	var headerState = AwaitingHeader
	var textContentLength int
	var propContentLength int

	for {
		line := source.Lbs.Readline()
		if len(line) == 0 {
			break
		}

		if string(line) == "\n" {
			if headerState == InHeader {
				if propContentLength > 0 {
					headerState = InProps
				} else if textContentLength > 0 {
					headerState = InText
				} else {
					//os.Stdout.WriteString("Awaiting header, no props or content\n")
					headerState = AwaitingHeader
				}
			} else if headerState == InProps && propParserState == propsEnd {
				if textContentLength > 0 {
					headerState = InText
				} else {
					//os.Stdout.WriteString("Awaiting header, props done, no content\n")
					headerState = AwaitingHeader
				}
			} else if headerState == InProps {
				croak("empty lines inside Props-Section should be processed directly in properties parser!")
			}
			os.Stdout.Write(line)

			if headerState == InText {
				os.Stdout.Write(source.Lbs.Read(textContentLength))
				os.Stdout.Write(source.Lbs.Readline())
				//os.Stdout.WriteString("Awaiting header after content\n")
				headerState = AwaitingHeader
			}
			continue
		}

		ss := StreamSection(line)
		if p = ss.payload("Revision-number"); p != nil {
			if headerState != AwaitingHeader {
				croak("headerState should be in InHeader, was: " + fmt.Sprint(headerState))
			}
			headerState = InHeader
			propContentLength = 0
			textContentLength = 0
			propParserState = awaitingNext

			fmt.Printf("Revision-number: %d\n", counter)
			v, _ := strconv.Atoi(string(p))
			renumbering[v] = counter
			counter++
		} else if p = ss.payload("Node-path"); p != nil {
			if headerState != AwaitingHeader {
				croak("headerState should be in InHeader, was: " + fmt.Sprint(headerState))
			}
			headerState = InHeader
			propContentLength = 0
			textContentLength = 0
			propParserState = awaitingNext

			os.Stdout.Write(line)
		} else if p = ss.payload("Text-content-length"); p != nil {
			textContentLength, _ = strconv.Atoi(string(p))
			os.Stdout.Write(line)
		} else if p = ss.payload("SVN-fs-dump-format-version"); p != nil {
			os.Stdout.Write(line)
		} else if p = ss.payload("UUID"); p != nil {
			os.Stdout.Write(line)
		} else if p = ss.payload("Prop-content-length"); p != nil {
			propContentLength, _ = strconv.Atoi(string(p))
			os.Stdout.Write(line)
			continue
		} else if p = ss.payload("Node-copyfrom-rev"); p != nil {
			v, _ := strconv.Atoi(string(p))
			fmt.Printf("Node-copyfrom-rev: %d\n", renumberBack(v))
		} else {
			if headerState == AwaitingHeader {
				os.Stdout.Write(line)
				continue
			}

			// A typical mergeinfo entry looks like this:
			// K 13
			// svn:mergeinfo
			// V 18
			// /branches/v1.0:4-6
			//                        <- Optional empty line
			// PROPS-END
			needsWrite := true

			if headerState == InProps {
				if propParserState == awaitingNext {
					if bytes.HasPrefix(line, []byte("K ")) {
						propParserState = awaitingKeyValue
					} else if bytes.HasPrefix(line, []byte("D ")) {
						propParserState = awaitingPropDelete
					} else if bytes.HasPrefix(line, []byte("PROPS-END")) {
						needsWrite = false
						propParserState = propsEnd
						os.Stdout.Write(line)
						if textContentLength > 0 {
							os.Stdout.Write(source.Lbs.Read(textContentLength))
							os.Stdout.Write(source.Lbs.Readline())
							//os.Stdout.WriteString("Awaiting header after content after PROPS-END\n")
							headerState = AwaitingHeader
						}
					} else {
						croak("unknown property entry begin: " + string(line))
					}
				} else if propParserState == awaitingPropDelete {
					propParserState = awaitingNext
				} else if propParserState == awaitingKeyValue {
					needsWrite = false
					performDefaultTransformation := true
					var mergeinfolength int
					for _, mergeproperty := range mergeProperties {
						if bytes.HasPrefix(line, []byte(mergeproperty)) {
							os.Stdout.Write(line)
							lengthline := source.Lbs.Readline()
							os.Stdout.Write(lengthline)
							mergeinfolength, _ = strconv.Atoi(string(bytes.Fields(lengthline)[1]))
							os.Stdout.Write(renumberMergeInfo(source.Lbs.Read(mergeinfolength), renumbering))
							// ignore trailing newline, already artificially appended in renumberMergeInfo
							source.Lbs.Readline()
							propParserState = awaitingNext
							performDefaultTransformation = false
							break
						}
					}
					if performDefaultTransformation {
						os.Stdout.Write(line)
						lengthline := source.Lbs.Readline()
						os.Stdout.Write(lengthline)
						mergeinfolength, _ = strconv.Atoi(string(bytes.Fields(lengthline)[1]))
						os.Stdout.Write(source.Lbs.Read(mergeinfolength))
						os.Stdout.Write(source.Lbs.Readline()) // trailing newline
						propParserState = awaitingNext
					}
				}
			}

			if needsWrite {
				os.Stdout.Write(line)
			}
		}
	}
}

func replace(source DumpfileSource, selection SubversionRange, transform string) {
	patternParts := strings.Split(transform[1:], transform[0:1])
	if len(patternParts) != 3 || patternParts[2] != "" {
		croak("ill-formed transform specification")
	}
	tre, err := regexp.Compile(patternParts[0])
	if err != nil {
		croak("illegal regular expression: %v", err)
	}

	innerreplace := func(header StreamSection, properties []byte, content []byte) []byte {
		newcontent := tre.ReplaceAll(content, []byte(patternParts[1]))
		if string(content) != string(newcontent) {
			header = header.setLength("Text-content", len(newcontent))
			header = header.setLength("Content", len(properties)+len(newcontent))
			header = header.stripChecksums()
		}

		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, newcontent...)
		return all
	}
	source.Report(selection, innerreplace, nil, true)
}

// Strip out ops defined by a revision selection and a path regexp.
func see(source DumpfileSource, selection SubversionRange) {
	props := ""
	seenode := func(header StreamSection, _, _ []byte) []byte {
		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<header: %q>\n", header)
		}
		path := header.payload("Node-path")
		if header.isDir(source) {
			path = append(path, os.PathSeparator)
		}
		frompath := header.payload("Node-copyfrom-path")
		fromrev := header.payload("Node-copyfrom-rev")
		action := header.payload("Node-action")
		if frompath != nil && fromrev != nil {
			if header.isDir(source) {
				frompath = append(frompath, os.PathSeparator)
			}
			path = append(path, []byte(fmt.Sprintf(" from %s:%s", fromrev, frompath))...)
			action = []byte("copy")
		}
		leader := fmt.Sprintf("%d-%d", source.Revision, source.Index)
		fmt.Printf("%-5s %-8s %s\n", leader, action, path)
		if props != "" {
			fmt.Printf("%-5s %-8s %s\n", leader, "propset", props)
		}
		props = ""
		return nil
	}
	seeprops := func(properties *Properties) {
		for _, skippable := range []string{"svn:log", "svn:date", "svn:author"} {
			if _, ok := properties.properties[skippable]; ok {
				return
			}
		}
		props = properties.String()
	}
	source.Report(selection, seenode, seeprops, false)
}

// Mutate log entries.
func setlog(source DumpfileSource, logpath string, selection SubversionRange) {
	fd, ok := os.Open(logpath)
	if ok != nil {
		croak("couldn't open " + logpath)
	}
	logpatch := NewLogfile(fd, &selection)
	loghook := func(prop *Properties) {
		_, haslog := prop.properties["svn:log"]
		if haslog && logpatch.Contains(source.Revision) {
			logentry := logpatch.comments[source.Revision]
			if string(logentry.author) != getAuthor(prop.properties) {
				croak("author of revision %d doesn't look right, aborting!\n", source.Revision)
			}
			prop.properties["svn:log"] = string(logentry.text)
		}
	}
	source.Report(selection, dumpall, loghook, true)
}

// Strip a portion of the dump file defined by a revision selection.
// Sift for ops defined by a revision selection and a path regexp.
func sift(source DumpfileSource, selection SubversionRange, patterns []string) {
	regexps := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		regexps[i] = regexp.MustCompile(pattern)
	}
	sifthook := func(header StreamSection, properties []byte, content []byte) []byte {
		matched := false
		for _, hd := range []string{"Node-path", "Node-copyfrom-path"} {
			nodepath := header.payload(hd)
			if nodepath != nil {
				for _, r := range regexps {
					if r.Match(nodepath) {
						matched = true
						break
					}
				}
			}
		}
		if matched {
			all := make([]byte, 0)
			all = append(all, []byte(header)...)
			all = append(all, properties...)
			all = append(all, content...)
			return all
		}
		return []byte{}
	}
	source.Report(selection, sifthook, nil, true)
}

func split(source DumpfileSource, selection SubversionRange, paths []string) {
	splithook := func(header StreamSection, properties []byte, content []byte) []byte {
		matches := false
		target := header.payload("Node-path")
		for _, path := range paths {
			if bytes.Equal(target, []byte(path)) {
				matches = true
			}
		}
		if matches {
			if p := string(properties); p != "" && p != "PROPS-END\n" {
				croak("can't split a node with nonempty properties (%v).", string(properties))
			}
			if debug >= debugPARSE {
				fmt.Fprintf(os.Stderr, "<split firing on %q>\n", header)
			}
			originalHeader := string(header)
			propdelim := ""
			if strings.Contains(originalHeader, "Prop-content") {
				propdelim = "PROPS-END\n\n"
			}
			copytarget := "Node-path: " + string(header.payload("Node-path"))
			copysource := "Node-copyfrom-path: " + string(header.payload("Node-copyfrom-path"))
			trunkCopy := strings.Replace(originalHeader, copytarget, copytarget+"/trunk", 1)
			branchesCopy := strings.Replace(originalHeader, copytarget, copytarget+"/branches", 1)
			tagsCopy := strings.Replace(originalHeader, copytarget, copytarget+"/tags", 1)
			if copysource != "" {
				trunkCopy = strings.Replace(trunkCopy, copysource, copysource+"/trunk", 1)
				branchesCopy = strings.Replace(branchesCopy, copysource, copysource+"/branches", 1)
				tagsCopy = strings.Replace(tagsCopy, copysource, copysource+"/tags", 1)
			}
			header = []byte(trunkCopy + propdelim + branchesCopy + propdelim + tagsCopy)
		}
		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, splithook, nil, true)
}

func strip(source DumpfileSource, selection SubversionRange, patterns []string) {
	innerstrip := func(header StreamSection, properties []byte, content []byte) []byte {
		// first check against the patterns, if any are given
		ok := true
		nodepath := header.payload("Node-path")
		if nodepath != nil {
			for _, pattern := range patterns {
				ok = false
				re := regexp.MustCompile(pattern)
				if re.Match(nodepath) {
					//os.Stderr.Write("strip skipping: %s\n", nodepath)
					ok = true
					break
				}
			}
		}
		if ok {
			if len(content) > 0 { //len([]nil == 0)
				// Avoid replacing symlinks, a reposurgeon sanity check barfs.
				if !bytes.HasPrefix(content, []byte("link ")) {
					tell := fmt.Sprintf("Revision is %d, file path is %s.\n",
						source.Revision, header.payload("Node-path"))
					content = []byte(tell)
					header = header.setLength("Text-content", len(content))
					header = header.setLength("Content", len(properties)+len(content))
				}
			}
			header = header.stripChecksums()
		}

		all := make([]byte, 0)
		all = append(all, []byte(header)...)
		all = append(all, properties...)
		all = append(all, content...)
		return all
	}
	source.Report(selection, innerstrip, nil, true)
}

// Select a portion of the dump file not defined by a revision selection.
func sselect(source DumpfileSource, selection SubversionRange) {
	doSelect(source, selection, false)
}

// Hack paths by swapping the top two components - if "structural" is on, be Subversion-aware
// and also attempt to merge spans of partial branch creations.
func swap(source DumpfileSource, selection SubversionRange, patterns []string, structural bool) {
	var match *regexp.Regexp
	if len(patterns) > 0 {
		match = regexp.MustCompile(patterns[0])
	}
	type parsedNode struct {
		role     string
		action   []byte
		isDelete bool
		isCopy   bool
		isDir    bool
	}
	wildcards := make(map[string]orderedStringSet)
	var wildcardKey string
	const wildcardMark = '*'
	stdlayout := func(payload []byte) bool {
		return bytes.HasPrefix(payload, []byte("trunk")) || bytes.HasPrefix(payload, []byte("tags")) || bytes.HasPrefix(payload, []byte("branches"))
	}
	swapper := func(sourcehdr string, path []byte, parsed parsedNode) []byte {
		// mergeinfo paths are rooted - leading slash should
		// be ignored, then restored.
		rooted := len(path) > 0 && (path[0] == byte(os.PathSeparator))
		if rooted {
			path = path[1:]
		}
		parts := bytes.Split(path, []byte{os.PathSeparator})
		if len(parts) >= 2 {
			top := string(parts[0])
			if structural {
				under := string(parts[1])
				if under == "trunk" {
					parts[0] = parts[1]
					parts[1] = []byte(top)
				} else if under == "branches" || under == "tags" {
					// Shift "branches" or "tags" to top level
					parts[0] = []byte(under)
					if len(parts) >= 3 {
						// This is where we capture information abnout what
						// branches and tags exist under a specified project
						// directory.
						if parsed.isDir && len(parts) == 3 {
							key := top + pathsep + string(parts[1])
							subbranch := string(parts[2])
							switch parsed.role {
							case "add":
								trackSet := wildcards[key]
								trackSet.Add(subbranch)
								wildcards[key] = trackSet
							case "delete":
								trackSet := wildcards[key]
								trackSet.Remove(subbranch)
								wildcards[key] = trackSet
							}
						}
						parts[1] = parts[2]
						parts[2] = []byte(top)
					} else {
						// If you're doing a structural swap and see a path
						// that looks like foo/branches or foo/tags, simply swapping
						// those cannot be correct.  By the premise of this operation,
						// foo should a directory name under some branch which isn't
						// specified here
						//
						// Figuring out the right thing to do here is tricky.
						if !parsed.isDir {
							// Probably never happens but let's be safe.
							parts[1] = []byte(top)
						} else {
							switch parsed.role {
							case "add":
								// Start tracking subbranches/subtags of foo,
								wildcards[string(path)] = newOrderedStringSet()
								// Then drop this path - nothing else needs doing.
								parts = nil
							case "delete":
								// Stop tracking subbranches/subtags of foo
								delete(wildcards, string(path))
								// Then drop this path - nothing else needs doing.
								parts = nil
							case "change":
								wildcardKey = string(path)
								parts[1] = []byte{wildcardMark}
								parts = append(parts, []byte(top))
							case "copy":
								if sourcehdr == "Node-copyfrom-path: " {
									wildcardKey = string(path)
									parts[1] = []byte{wildcardMark}
									parts = append(parts, []byte(top))
								}
							case "mergeinfo":
								croak("r%d-%d: unexpected mergeinfo of path %s",
									source.Revision, source.Index, path)
							default:
								croak("r%d-%d: unexpected action %s on path %s",
									source.Revision, source.Index, parsed.role, path)
							}
						}
					}
				}
			} else { // naive swap
				parts[0] = parts[1]
				parts[1] = []byte(top)
			}
		}
		if rooted {
			parts[0] = append([]byte{os.PathSeparator}, parts[0]...)
		}
		return bytes.Join(parts, []byte{os.PathSeparator})
	}
	revhook := func(props *Properties) {
		swapped := make([]string, 0)
		for _, mergeproperty := range mergeProperties {
			if m, ok := props.properties[mergeproperty]; ok {
				for _, part := range bytes.Split([]byte(m), []byte{'\n'}) {
					var dummy parsedNode
					dummy.role = "mergeinfoi"
					swapped = append(swapped, string(swapper("", part, dummy)))
				}
				props.properties[mergeproperty] = strings.Join(swapped, ":")
			}
		}
		if source.Revision == 1 && props.Contains("svn:log") {
			props.properties["svn:log"] = "Synthetic branch-structure creation.\n"
		}
		// We leave author and date alone.  This will get
		// dropped in the git version; it's only being
		// generated so reposurgeon doesn't get confused about
		// the branch structure.
	}
	var oldval, newval []byte
	var swaplatch bool // Ugh, but less than global
	type copyPair struct {
		nodePath     string
		copyfromPath string
	}
	var thisCopyPair, lastCopyPair copyPair
	var thisDelete, lastDelete string
	nodehook := func(header StreamSection, properties []byte, content []byte) []byte {
		all := make([]byte, 0)
		// FIXME: Unconditionally prepending this won't work on partial swaps.
		const swapHeader = `Node-path: branches
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: tags
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END

`
		nodePath := header.payload("Node-path")
		if !swaplatch {
			swaplatch = true
			if !stdlayout(nodePath) {
				all = []byte(swapHeader)
			}
		}

		coalesced := false
		var parsed parsedNode
		parsed.action = header.payload("Node-action")
		parsed.isDelete = bytes.Equal(parsed.action, []byte("delete"))
		parsed.isCopy = header.index("Node-copyfrom-path") != -1
		parsed.isDir = header.isDir(source)
		parsed.role = string(parsed.action)
		if parsed.isCopy {
			parsed.role = "copy"
		}
		if match == nil || match.Match(nodePath) {
			/* FIXME: This should only drop nodes, but it looses entire commits
			// Special handling of operations on bare project directories
			if bytes.Count(nodePath, []byte(pathsep)) == 0 && !stdlayout(nodePath) {
				// Don't retain creation of project
				// directories, these are replaced by
				// the creation of the top-level
				// trunk/tags/branches directories in
				// the swapped hierarchy.  Alas,
				// there's no safe place to put the
				// metadata.
				if bytes.Equal(parsed.action, []byte("add")) {
					fmt.Fprintf(os.Stderr, "XXX Removing %s (%d-%d)\n", nodePath, source.Revision, source.Index)
					return nil
				}
			}
			*/
			wildcardKey = ""
			header, newval, oldval = header.replaceHook("Node-path: ", func(path []byte) []byte {
				return swapper("Node-path: ", path, parsed)
			})
			header, newval, oldval = header.replaceHook("Node-path: ", func(in []byte) []byte {
				branchcopy := parsed.isDir && (parsed.isCopy || parsed.isDelete)
				parts := bytes.Split(in, []byte{os.PathSeparator})
				if structural && branchcopy && len(parts) == 3 {
					top := string(parts[0])
					if top == "branches" || top == "tags" {
						parts = parts[:2]
					}
				}
				return bytes.Join(parts, []byte{os.PathSeparator})
			})
			if oldval != nil && newval == nil {
				return nil
			}
			coalesced = !bytes.Equal(oldval, newval)
			if coalesced {
				if parsed.isDelete {
					thisDelete = string(newval)
				} else {
					thisCopyPair = copyPair{string(newval), ""}
				}
			}
		}
		if match == nil || match.Match(header.payload("Node-copyfrom-path")) {
			header, newval, oldval = header.replaceHook("Node-copyfrom-path: ", func(path []byte) []byte {
				return swapper("Node-copyfrom-path: ", path, parsed)
			})
			if bytes.Contains(newval, []byte{wildcardMark}) {
				header, _, _ = header.replaceHook("Node-path: ", func(in []byte) []byte {
					return append(in, os.PathSeparator, wildcardMark)
				})
			}
			if !coalesced {
				// Actions at end of copy or delete spans could go here,
				// but that action would also have to fire at the end of swap().
				// Reset the clique state.
				var zeroCopyPair copyPair
				lastCopyPair = zeroCopyPair
				lastDelete = ""
			} else if !parsed.isDelete {
				header, newval, oldval = header.replaceHook("Node-copyfrom-path: ", func(in []byte) []byte {
					parts := bytes.Split(in, []byte{os.PathSeparator})
					if len(parts) == 3 {
						top := string(parts[0])
						if top == "trunk" {
							parts = parts[:1]
						} else if top == "branches" || top == "tags" {
							parts = parts[:2]
						}
					}
					return bytes.Join(parts, []byte{os.PathSeparator})
				})
				thisCopyPair.copyfromPath = string(newval)

				if lastCopyPair == thisCopyPair {
					// We're looking at the second or
					// later copy in a span of nodes that
					// refer to the same global branch;
					// we can drop it.
					return nil
				}
				lastCopyPair = thisCopyPair
			} else { // isDelete
				if lastDelete == thisDelete {
					// We're looking at the second or
					// later delete in a span of nodes that
					// refer to the same global branch;
					// we can drop it.
					return nil
				}
				lastDelete = thisDelete
			}
		}

		if wildcardKey == "" {
			all = append(all, []byte(header)...)
			all = append(all, properties...)
			all = append(all, content...)
		} else {
			for _, subbranch := range wildcards[wildcardKey].Iterate() {
				clone := bytes.Replace(header,
					[]byte{wildcardMark}, []byte(subbranch),
					-1)
				all = append(all, clone...)
				all = append(all, properties...)
				all = append(all, content...)
			}
		}

		return all
	}
	source.Report(selection, nodehook, revhook, true)
}

// Neutralize the input test load
func testify(source DumpfileSource) {
	const NeutralUser = "fred"
	const NeutralUserLen = len(NeutralUser)
	counter := base
	var p []byte
	var state, oldAutherLen, oldPropLen, oldContentLen int
	var headerBuf []byte // need buffer to edit Prop-content-length and Content-length
	var inRevHeader, saveToHeaderBuf bool
	// since Go doesn't have a ternary operator, we need to create these helper funcs
	getPropLen := func(saveToHeaderBuf bool, line []byte) []byte {
		if counter > 1 && inRevHeader && !saveToHeaderBuf { // first rev doesn't have an author
			return StreamSection(line).payload("Prop-content-length")
		}
		return nil
	}
	getContentLen := func(saveToHeaderBuf bool, line []byte) []byte {
		if saveToHeaderBuf {
			return StreamSection(line).payload("Content-length")
		}
		return nil
	}

	for {
		line := source.Lbs.Readline()
		if len(line) == 0 {
			break
		}
		if p = StreamSection(line).payload("UUID"); p != nil && source.Lbs.linenumber <= 10 {
			line = make([]byte, 0)
		} else if p = StreamSection(line).payload("Revision-number"); p != nil {
			counter++
			inRevHeader = true
		} else if p = getPropLen(saveToHeaderBuf, line); p != nil {
			saveToHeaderBuf = true
			headerBuf = make([]byte, 0)
			line = make([]byte, 0)
			oldPropLen, _ = strconv.Atoi(string(p))
		} else if p = getContentLen(saveToHeaderBuf, line); p != nil {
			line = make([]byte, 0)
			oldContentLen, _ = strconv.Atoi(string(p))
		} else if bytes.HasPrefix(line, []byte("svn:author")) {
			state = 1
		} else if state == 1 && bytes.HasPrefix(line, []byte("V ")) {
			oldAutherLen, _ = strconv.Atoi(string(line[2 : len(line)-1]))
			headerBuf = append([]byte(fmt.Sprintf("Prop-content-length: %d\nContent-length: %d\n",
				(oldPropLen+NeutralUserLen-oldAutherLen),
				(oldContentLen+NeutralUserLen-oldAutherLen))), headerBuf...)
			line = append(headerBuf, []byte(fmt.Sprintf("V %d\n", NeutralUserLen))...)
			saveToHeaderBuf = false
			inRevHeader = false
			state = 2
		} else if bytes.HasPrefix(line, []byte("svn:date")) {
			state = 4
		} else if bytes.HasPrefix(line, []byte("PROPS-END")) {
			state = 0
		}

		if state == 3 {
			line = []byte(NeutralUser + "\n")
			state = 0
		} else if state == 6 {
			t := time.Unix(int64((counter-1)*10), 0).UTC().Format(time.RFC3339)
			t2 := t[:19] + ".000000Z"
			line = []byte(t2 + "\n")
			state = 0
		}

		if saveToHeaderBuf {
			headerBuf = append(headerBuf, line...)
		} else {
			os.Stdout.Write(line)
		}

		if state >= 2 {
			state++
		}
	}
}

func main() {
	selection := NewSubversionRange("0:HEAD")
	var logentries string
	var rangestr string
	var infile string
	input := os.Stdin
	flag.IntVar(&debug, "d", 0, "enable debug messages")
	flag.IntVar(&debug, "debug", 0, "enable debug messages")
	flag.StringVar(&infile, "i", "", "set input file")
	flag.StringVar(&infile, "infile", "", "set input file")
	flag.StringVar(&logentries, "l", "", "pass in log patch")
	flag.StringVar(&logentries, "logentries", "", "pass in log patch")
	flag.BoolVar(&quiet, "q", false, "disable progress messages")
	flag.BoolVar(&quiet, "quiet", false, "disable progress messages")
	flag.StringVar(&rangestr, "r", "", "set selection range")
	flag.StringVar(&rangestr, "range", "", "set selection range")
	flag.IntVar(&base, "b", 0, "base value to renumber from")
	flag.IntVar(&base, "base", 0, "base value to renumber from")
	flag.StringVar(&tag, "t", "", "set error tag")
	flag.StringVar(&tag, "tag", "", "set error tag")
	flag.BoolVar(&docgen, "docgen", false, "generate asciidoc from embedded help")
	flag.Parse()

	if docgen {
		dumpDocs()
		os.Exit(0)
	}

	if tag != "" {
		tag = "(" + tag + ")"
	}
	if rangestr != "" {
		selection = NewSubversionRange(rangestr)
	}
	if infile != "" {
		var err error
		input, err = os.Open(infile)
		if err != nil {
			fmt.Fprint(os.Stderr, "Input file open failed.\n")
			os.Exit(1)
		}
	}
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<selection: %v>\n", selection)
	}

	if flag.NArg() == 0 {
		fmt.Fprint(os.Stderr, "Type 'repocutter help' for usage.\n")
		os.Exit(1)
	} else if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<command=%s>\n", flag.Arg(0))
	}
	var baton *Baton
	if flag.Arg(0) != "help" && flag.Arg(0) != "version" {
		if !quiet {
			baton = NewBaton(oneliners[flag.Arg(0)], "done")
		} else {
			baton = nil
		}
	}

	assertNoArgs := func() {
		if len(flag.Args()) != 1 {
			croak("extra arguments detected after command keyword!\n")
		}
	}

	// Undocumented: Debug level can be set with a "Debug-level:" header
	// immediately after a Revision-number header.

	switch flag.Arg(0) {
	case "closure":
		closure(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "deselect":
		assertNoArgs()
		deselect(NewDumpfileSource(input, baton), selection)
	case "expunge":
		expunge(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "help":
		if len(flag.Args()) == 1 {
			os.Stdout.WriteString(doc)
			break
		}
		if cdoc, ok := helpdict[flag.Arg(1)]; ok {
			os.Stdout.WriteString(cdoc)
			break
		}
		croak("no such command\n")
	case "log":
		assertNoArgs()
		log(NewDumpfileSource(input, baton), selection)
	case "obscure":
		assertNoArgs()
		obscure(NewNameSequence(), NewDumpfileSource(input, baton), selection)
	case "pathlist":
		pathlist(NewDumpfileSource(input, baton), selection)
	case "pathrename":
		pathrename(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "pop":
		assertNoArgs()
		pop(NewDumpfileSource(input, baton), selection)
	case "propdel":
		propdel(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "propset":
		propset(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "proprename":
		proprename(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "reduce":
		reduce(NewDumpfileSource(input, baton), selection)
	case "push":
		push(NewDumpfileSource(input, baton), selection, flag.Args()[1])
	case "renumber":
		assertNoArgs()
		renumber(NewDumpfileSource(input, baton))
	case "replace":
		replace(NewDumpfileSource(input, baton), selection, flag.Args()[1])
	case "see":
		assertNoArgs()
		see(NewDumpfileSource(input, baton), selection)
	case "select":
		assertNoArgs()
		sselect(NewDumpfileSource(input, baton), selection)
	case "setlog":
		if logentries == "" {
			fmt.Fprintf(os.Stderr, "repocutter: setlog requires a log entries file.\n")
			os.Exit(1)
		}
		setlog(NewDumpfileSource(input, baton), logentries, selection)
	case "sift":
		sift(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "split":
		split(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "strip":
		strip(NewDumpfileSource(input, baton), selection, flag.Args()[1:])
	case "swap":
		swap(NewDumpfileSource(input, baton), selection, flag.Args()[1:], false)
	case "swapsvn":
		swap(NewDumpfileSource(input, baton), selection, flag.Args()[1:], true)
	case "testify":
		assertNoArgs()
		testify(NewDumpfileSource(input, baton))
	case "version":
		assertNoArgs()
		fmt.Println(version)
	default:
		fmt.Fprintf(os.Stderr, "repocutter: \"%s\": unknown subcommand\n", flag.Arg(0))
		os.Exit(1)
	}
	if baton != nil {
		baton.End("")
	}
}
