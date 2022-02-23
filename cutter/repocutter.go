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
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	term "golang.org/x/term" // For IsTerminal()
)

const linesep = "\n"

var dochead = `repocutter - stream surgery on SVN dump files
general usage: repocutter [-q] [-r SELECTION] SUBCOMMAND

In all commands, the -r (or --range) option limits the selection of revisions
and nodes over which an operation will be performed. A selection consists of one
or more comma-separated ranges.  A range may consist of an endpoint or a colon-
separated pair of endpoints.  An endpoint may consist of an integer identifying
a revision, the special name HEAD for the head (last) revision, or a node
specification of the form rev.node where rev is an integer revision number and
node in a 1-origin node index.

Filename PATTERN arguments are regular expressions to match pathnames,
constrained so that each match must be a path segment or a sequence of path
segments; that is, the left end must be either at the start of path or
immediately following a /, and the right end must precede a / or be at end
of string.  With a leading ^ the match is constrained to be a leading
sequence of the pathname; with a trailing $, a trailing one.

The -f/-fixed option disables regexp compilation of PATTERN arguments, treating
them as literal strings.

Normally, each subcommand produces a progress spinner on standard error; each
turn means another revision has been filtered. The -q (or --quiet) option
suppresses this.

Type 'repocutter help <subcommand>' for help on a specific subcommand.

Available subcommands and help topics:

`

// Translated from the 2017-12-13 version of repocutter,
// which began life as 'svncutter' in 2009.  The obsolete
// 'squash' command has been omitted.

var debug int

const debugLOGIC = 1
const debugPARSE = 2

var quiet bool

var helpdict = map[string]struct {
	oneliner string
	text     string
}{
	"closure": {
		"Compute the transitive closure of a path set",
		`closure: usage: repocutter [-q] closure PATH...

The 'closure' subcommand computes the transitive closure of a path set under the
relation 'copies from' - that is, with the smallest set of additional paths such
that every copy-from source is in the set.
`},
	"deselect": {
		"Deselecting revisions",
		`deselect: usage: repocutter [-q] [-r SELECTION] deselect

The 'deselect' subcommand selects a range and permits only revisions and nodes
NOT in that range to pass to standard output.  Any mergeinfo properties in other
revisions are updated so they no longer refer to dropped revisiomns.
`},
	"expunge": {
		"Expunge operations by Node-path header",
		`expunge: usage: repocutter [-r SELECTION ] [-f|-fixed] expunge PATTERN...

Delete all operations with Node-path or Node-copyfrom-path headers matching
specified Golang regular expressions (opposite of 'sift').  Any revision
left with no Node records after this filtering has its Revision record dropped as
well. Mergeinfo properties in all revisions are updated so they no longer refer
to dropped revisions.
`},
	"filecopy": {
		"Resolve filecopy operations on a stream.",
		`filecopy: usage: repocutter [-f] [-r SELECTION] filecopy [BASENAME]

For each node in the revision range, stash the current version of the
node-path's content.  For each later file copy operation with that source,
replace the file copy with an explicit add/change using the stashed content.

With the -f flag and a BASENAME argument, require the source basename
to be as specified.  Otherwise, with -f and no BASENAME, require a
match of source to targwt on basename only rather than the full path.
This may be required in order to extract filecopies from branches.

Restricting the range holds down the memory requirement of this tool,
which in the worst (and default) 1:$ case will keep a copy of evert blob
in the repository until it's done processing the stream.
`},
	"log": {
		"Extracting log entries",
		`log: usage: repocutter [-r SELECTION] log

Generate a log report, same format as the output of svn log on a
repository, to standard output.
`},
	"obscure": {
		"Obscure pathnames",
		`obscure: usage: repocutter [-r SELECTION] obscure

Replace path segments and committer IDs with arbitrary but consistent
names in order to obscure them. The replacement algorithm is tuned to
make the replacements readily distinguishable by eyeball.  This
transform can be restricted by a selection set.
`},
	"pathlist": {
		"List all distinct paths in a stream",
		`pathlist: usage: repocutter [-r SELECTION ] pathlist

List all distinct node-paths in the stream, once each, in the order first
encountered.
`},
	"pathrename": {
		"Transform path headers with a regexp replace",
		`pathrename: usage: repocutter [-r SELECTION ] pathrename {FROM TO}+

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

All mergeinfo properties are updated in accordance with the path renames,
`},
	"pop": {
		"Pop the first segment off each path",
		`pop: usage: repocutter pop [-f|-fixed] [PATTERN]

Pop initial segment off each path matching PATTERN - by default, all paths.

May be useful after a sift command to turn a dump from a subproject
stripped from a dump for a multiple-project repository into the normal
form with trunk/tags/branches at the top level.

This transform cannot be restricted by a selection set, as it is not possible to guarantee
that copyfro paths and mergeinfo properties will be modified consistently in the presence of
that kind of restriction.

Mergeinfo properties in all revisions are updated, as well as path and copyfrom parts.
`},
	"propclean": {
		"Turn off executable bit on all files with specified suffixes",
		`ppropclean: usage: repocutter [-r SELECTION ] [-p PROPERTY] propclean [SUFFIXES]

Every path with a suffix matching one of SUFFIXES gets a property turned
off.  The default property is svn:executable; some Subversion front ends spam it.
Another property may be set with the -p option.
`},
	"propdel": {
		"Deleting revision properties",
		`propdel: usage: repocutter [-r SELECTION] propdel PROPNAME...

Delete the property PROPNAME. May be restricted by a revision
selection. You may specify multiple properties to be deleted.
`},
	"proprename": {
		"Renaming revision properties",
		`proprename: usage: repocutter [-r SELECTION] proprename OLDNAME->NEWNAME...

Rename the property OLDNAME to NEWNAME. May be restricted by a
revision selection. You may specify multiple properties to be renamed.
`},
	"propset": {
		"Setting revision properties",
		`propset: usage: repocutter [-r SELECTION] propset PROPNAME=PROPVAL...

Set the property PROPNAME to PROPVAL.

May be restricted by a revision selection. Note that specifying only a revision
will cause the property  to be seet on the revision properties and on all nodes
in the rtevision; you'll probably want to specify a node index.

You may specify multiple property settings.
`},
	"push": {
		"Push a first segment onto each matching path",
		`push: usage: repocutter push [-s segment] [-f] [PATTERN...]

Push an initial segment onto each matching path. Normally used to add a
"trunk" prefix to every path in a flat repository.  The -s option can be used
rton set a different initial segment.

This transform cannot be restricted by a selection set, as it is not
possible to guarantee that copyfro paths and mergeinfo properties will
be modified consistently in the presence of that kind of restriction.

Mergeinfo properties in all revisions are updated toi refer to the
new pathnames.
`},
	"reduce": {
		"Topologically reduce a dump.",
		`reduce: usage: repocutter [-r selection] reduce

Strip revisions out of a dump so the only parts left those likely to
be relevant to a conversion problem. This is done by dropping every
node that consists of a change on a file and has no property settings.
Mergeinfo properties in all revisions are updated so they no longer refer
to dropped revisions.
`},
	"renumber": {
		"Renumber revisions so they're contiguous",
		`renumber: usage: repocutter renumber

Renumber all revisions, patching Node-copyfrom headers as required.
Any selection option is ignored. Takes no arguments.  The -b option
can be used to set the base to renumber from, defaulting to 0.
`},
	"replace": {
		"Regexp replace in blobs",
		`replace: usage: repocutter replace /REGEXP/REPLACE/

Perform a regular expression search/replace on blob content. The first
character of the argument (normally /) is treated as the end delimiter
for the regular-expression and replacement parts. This transform can be
restricted by a selection set.
`},
	"see": {
		"Report only essential topological information",
		`see: usage: repocutter [-r SELECTION] see

Render a very condensed report on the repository node structure, mainly
useful for examining strange and pathological repositories.  File content
is ignored.  You get one line per repository operation, reporting the
revision, operation type, file path, and the copy source (if any).
Directory paths are distinguished by a trailing slash.  The 'copy'
operation is really an 'add' with a directory source and target;
the display name is changed to make them easier to see. This report
can be restricted by a selection set.
`},
	"select": {
		"Selecting revisions",
		`select: usage: repocutter [-q] [-r SELECTION] select

The 'select' subcommand selects a range and permits only revisions and
nodes in that range to pass to standard output.  A range beginning with 0
includes the dumpfile header. Mergeinfo properties in all revisions are
updated so they no longer refer to omitted revisions.
`},
	"setcopyfrom": {
		"Set the copyfrom path.",
		`setcopyfrom: usage: repocutter {-r SELECTION} setcopyfrom PATH

In the specified revisions, replace the Node-copyfrom-path with the specified PATH.
Does not alter mergeinfo properties as a side effect.  Terminates with error if any
selected node is not a copy.
`},
	"setlog": {
		"Mutating log entries",
		`setlog: usage: repocutter [-r SELECTION] -logentries=LOGFILE setlog

Replace the log entries in the input dumpfile with the corresponding entries
in the LOGFILE, which should be in the format of an svn log output.
Replacements may be restricted to a specified range.
`},
	"setpath": {
		"Set the node path.",
		`setcopyfrom: usage: repocutter {-r SELECTION} setpath PATH

In the specified revisions, replace the Node-path with the specified PATH.
Does not alter mergeinfo properties as a side effect.
`},
	"sift": {
		"Sift for operations by Node-path header",
		`sift: usage: repocutter [-r SELECTION] [-f|-fixed] sift PATTERN...

Delete all operations with either Node-path or Node-copyfrom-path headers *not*
matching specified Golang regular expressions (opposite of 'expunge').
Any revision left with no Node records after this filtering has its Revision record
removed as well. Mergeinfo properties in all revisions are updated so they no longer refer
to dropped revisions.

This transform can be restricted by a selection set.
`},
	"skipcopy": {
		"Skip an intermediate copy chain between specified revisions",
		`skipcopy: usage: repocutter {-r selection} skipcopy

Replace the source revision and path of a copy at the upper end of the selection
with the source revisions and path of a copy at the lower end. Fails unless both
revisions are copies.  Used to remove an unwanted intermediate copy or
copies.
`},
	"strip": {
		"Replace content with unique cookies, preserving structure",
		`strip: usage: repocutter [-r SELECTION] strip [-f|-fixed] [PATTERN...]

Replace content with unique generated cookies on all node paths matching
the specified regular expressions; if no expressions are given, match all
paths.

This command is useful for reducing the bulk of a stream without touching
its metdata, so you can doio test conversions more quickly.
`},
	"swap": {
		"Swap first two components of pathnames",
		`swap: usage: repocutter [-r SELECTION] swap [-f|-fixed] [PATTERN]

Swap the top two elements of each pathname in every revision in the
selection set. Useful following a sift operation for straightening out
a common form of multi-project repository.  If a PATTERN argument is given,
only paths matching it are swapped.

`},
	"swapsvn": {
		"Subversion structure-aware swap",
		`swapsvn: usage: repocutter [-r SELECTION] swapsvn [-f|-fixed] [PATTERN]

Like swap, but is aware of Subversion structure.  Used for transforming
multiproject repositories into a standard layout with trunk, tags, and
branches at the top level.

Fires when the second component of a matching path is "trunk", "branches",
or "tags", or the path consists of a single segment that is a top-level
project directory; passes through all paths for this is not so unaltered.

Top-level project directories with properties or comments make this command
die (return status 1) with an error message on stderr; otherwise these
directories are silently discarded.

Otherwise, swaps "trunk" and the top-level (project) directory
straight up.  For tags and branches, the following *two* components
are swapped to the top.  thus, "foo/branches/release23" becomes
"branches/release23/foo", putting the project directory beneath the
branch.

Also fires when an entire project directory is copied; this is transformed
into a copy of trunk and copies of each subbranch and tag that exists.

After the swap, there are attempts to recognize spans of copies
into branch directories, and copies into tag subdirectories that are
parallel in all top-level (project) directories. These are coalesced
into single copies in the inverted structure.  No attempts is made
to coalesce deletes; the user must manually trim unneeded branches.

Accordingly, copies with three-segment sources and three-segment
targets are transformed; for tags/ and branches/ paths the last
segment (the subdirectory below the branch name) is dropped, Following
copies are skipped.

This has two minor negative consequences. One is that metadata
belonging to all deletes or copies after the first one in a coalesced
span is lost.  The other is that branches and tags local to
individual project directories are promoted to global branches and
tags across the entire transformed repository; no content is lost this
way.

Parallel rename sequences are also coalesced.

If a PATTERN argument is given, only paths matching the pattern are swapped.

Note that the result of swapping does not have initial trunk/branches/tags
directory creations and can thus not be fed directly to svnload. reposurgeon
copes with this, but Subversion will not.

Merfeinfo propertied are updated to use the swapped path names.

This transform can be restricted by a selection set.
`},
	"testify": {
		"Massage a stream file into a neutralized test load",
		`testify: usage: repocutter [-r SELECTION] testify

Replace commit timestamps with a monotonically increasing clock tick
starting at the Unix epoch and advancing by 10 seconds per commit.
Replace all attributions with 'fred'.  Discard the repository UUID.
Use this to neutralize procedurally-generated streams so they can be
compared. This transform can be restricted by a selection set.
`},
	"version": {
		"Report repocutter's version",
		`version: usage: version

Report major and minor repocutter version.
`},
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
	"propclean",

	"expunge",
	"sift",
	"closure",

	"pathlist",
	"pathrename",
	"setpath",
	"setcopyfrom",
	"pop",
	"push",
	"filecopy",
	"skipcopy",

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
	if len(narrativeOrder) != len(helpdict) {
		os.Stderr.WriteString("repocutter: documentation sanity check failed.\n")
		os.Exit(1)
	}
	re := regexp.MustCompile("([a-z][a-z]*):[^\n]*\n")
	for _, item := range narrativeOrder {
		text := helpdict[item].text
		text = re.ReplaceAllString(text, `${1}::`)
		text = strings.Replace(text, "\n\n", "\n+\n", -1)
		os.Stdout.WriteString(text)
		os.Stdout.WriteString("\n")
	}
}

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
	lbs.linenumber += strings.Count(string(text), linesep)
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

// Handle revision-range specifications

// SubversionEndpoint - represent as Subversion revision or revision.node spec
type SubversionEndpoint struct {
	rev  int
	node int
}

// Equals - are the components of two endoints equal?
func (s SubversionEndpoint) Equals(t SubversionEndpoint) bool {
	if s.node == 0 || t.node == 0 {
		croak("comparing %v=%v a full node specification with node index is required", t, s)
	}
	return s.rev == t.rev && s.node == t.node
}

// Stringer is the textualization method for interval endpoints
func (s SubversionEndpoint) Stringer() string {
	out := fmt.Sprintf("%d", s.rev)
	if s.node != 0 {
		out += fmt.Sprintf(".%d", s.node)
	}
	return out
}

// SubversionRange - represent a polyrange of Subversion commit numbers
type SubversionRange struct {
	intervals [][2]SubversionEndpoint
}

// NewSubversionRange - create a new polyrange object
func NewSubversionRange(txt string) SubversionRange {
	var s SubversionRange
	s.intervals = make([][2]SubversionEndpoint, 0)
	var upperbound int
	if txt == "" {
		return s
	}
	for _, item := range strings.Split(txt, ",") {
		var parts [2]SubversionEndpoint
		if strings.Contains(item, "-") {
			croak("use ':' for version ranges instead of '-'")
		}

		if strings.Contains(item, ":") {
			fields := strings.Split(item, ":")
			if fields[0] == "HEAD" {
				croak("can't accept HEAD as lower bound of a range.")
			}
			subfields := strings.Split(fields[0], ".")
			parts[0].rev, _ = strconv.Atoi(subfields[0])
			if len(subfields) > 1 {
				parts[0].node, _ = strconv.Atoi(subfields[1])
			}
			if fields[1] == "HEAD" {
				// Be on safe side - could be a 32-bit machine
				parts[1].rev = math.MaxInt32
			} else {
				subfields = strings.Split(fields[1], ".")
				parts[1].rev, _ = strconv.Atoi(subfields[0])
				if len(subfields) > 1 {
					parts[1].node, _ = strconv.Atoi(subfields[1])
				}
			}
		} else {
			fields := strings.Split(item, ".")
			parts[0].rev, _ = strconv.Atoi(fields[0])
			parts[1].rev, _ = strconv.Atoi(fields[0])
			if len(fields) > 1 {
				parts[0].node, _ = strconv.Atoi(fields[1])
				parts[1].node, _ = strconv.Atoi(fields[1])
			}
		}
		if parts[0].rev >= upperbound {
			upperbound = parts[0].rev
		} else {
			croak("ill-formed range specification")
		}
		s.intervals = append(s.intervals, parts)
	}
	return s
}

// ContainsRevision - does this range contain a specified revision?
func (s *SubversionRange) ContainsRevision(rev int) bool {
	for _, interval := range s.intervals {
		if rev >= interval[0].rev && rev <= interval[1].rev {
			return true
		}
	}
	return false
}

// ContainsNode - does this range contain a specified revision and node?
func (s *SubversionRange) ContainsNode(rev int, node int) bool {
	var interval [2]SubversionEndpoint
	for _, interval = range s.intervals {
		if rev >= interval[0].rev && rev <= interval[1].rev {
			if rev == interval[0].rev && node < interval[0].node {
				continue
			}
			// Omitting a node part in a specification becomes a zero
			// index, which matches all nodes *and* (in a property hook)
			// the revision properties as well.
			if rev == interval[1].rev && interval[1].node != 0 && node > interval[1].node {
				continue
			}
			return true
		}
	}
	return false
}

// Lowerbound - what is the lowest revision in the spec?
func (s *SubversionRange) Lowerbound() SubversionEndpoint {
	return s.intervals[0][0]
}

// Upperbound - what is the uppermost revision in the spec?
func (s *SubversionRange) Upperbound() SubversionEndpoint {
	return s.intervals[len(s.intervals)-1][1]
}

// dump exists because there are two different textualizations,
// one using : for ranges and the other using -.
func (s *SubversionRange) dump(rangesep string) string {
	if len(s.intervals) == 0 {
		return ""
	}
	out := ""
	for _, interval := range s.intervals {
		if interval[0] == interval[1] {
			out += interval[0].Stringer()
		} else {
			out += interval[0].Stringer() + rangesep + interval[1].Stringer()
		}
		out += ","
	}
	return out[:len(out)-1]
}

// Stringer is the texturalization method for Subversion ranges
func (s SubversionRange) Stringer() string {
	return s.dump(":")
}

// Optimize compacts a range as much as possible
func (s *SubversionRange) Optimize() {
	i := 0
	for {
		// Have we merged enough entries that we've run out of list?
		if i >= len(s.intervals)-1 {
			break
		}
		// Nope, try to merge the endpoint or range at i with its right-hand neighbor
		if s.intervals[i+1][0] == s.intervals[i][1] || s.intervals[i+1][0].rev == s.intervals[i][1].rev+1 {
			s.intervals = append(s.intervals[:i], append([][2]SubversionEndpoint{[2]SubversionEndpoint{s.intervals[i][0], s.intervals[i+1][1]}}, s.intervals[i+2:]...)...)
		} else {
			i++
		}
	}
}

// Property handling

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

// NonEmpty is the obvious predicate
func (props *Properties) NonEmpty() bool {
	return len(props.propkeys) > 0
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
	if props == nil || !props.NonEmpty() {
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

// Delete - delete the specified property
func (props *Properties) Delete(key string) {
	delete(props.properties, key)
	for delindex, item := range props.propkeys {
		if item == key {
			props.propkeys = append(props.propkeys[:delindex], props.propkeys[delindex+1:]...)
			break
		}
	}
	for delindex, item := range props.propdelkeys {
		if item == key {
			props.propdelkeys = append(props.propdelkeys[:delindex], props.propdelkeys[delindex+1:]...)
			break
		}
	}
}

// MutateMergeinfo mutates mergeinfo paths and ranges through a hook function
func (props *Properties) MutateMergeinfo(mutator func(string, string) (string, string)) {
	// The svnmerge-integrated property is set by svmerge.py.
	// Its semantics are poorly documented, but we process it
	// exactly like svn:mergeinfo and punt that problem to reposurgeon
	// on the "first, doo no harm" principle.
	for _, mergeproperty := range []string{"svn:mergeinfo", "svnmerge-integrated"} {
		if oldval, present := props.properties[mergeproperty]; present {
			mergeinfo := string(oldval)
			var buffer bytes.Buffer
			if len(mergeinfo) != 0 {
				for _, line := range strings.Split(mergeinfo, "\n") {
					if strings.Contains(line, ":") {
						lastidx := strings.LastIndex(line, ":")
						path, revrange := line[:lastidx], line[lastidx+1:]
						rooted := false
						if path[0] == os.PathSeparator {
							rooted = true
							path = path[1:]
						}
						newpath, newrange := mutator(path, revrange)
						if newpath == "" || newrange == "" {
							continue
						}
						if rooted {
							buffer.WriteByte(byte(os.PathSeparator))
						}
						buffer.WriteString(newpath)
						buffer.WriteString(":")
						buffer.WriteString(newrange)
					} else {
						buffer.WriteString(line)
					}
					buffer.WriteString(linesep)
				}
			}
			// Discard last newline, because the V length of the property
			// does not count it - but does count interior \n in
			// multiline values.  The guard is required because empty
			// mefeinfo properties have been seen in the wild.
			if buffer.Len() > 0 {
				buffer.Truncate(buffer.Len() - 1)
			}
			if r := buffer.String(); r == "" {
				props.Delete(mergeproperty)
			} else {
				props.properties[mergeproperty] = r
			}
		}
	}
}

func (props *Properties) getAuthor() string {
	if author, ok := props.properties["svn:author"]; ok {
		return author
	}
	return "(no author)"
}

// Miscellaneous helper functions

// MergeinfoInterval carries both limit information and a heritability flag
type MergeinfoInterval struct {
	Lower          int
	Upper          int
	NonInheritable bool
}

// MergeinfoRange is the direct analog of a SubversionRange
type MergeinfoRange struct {
	intervals []MergeinfoInterval
}

func parseMergeinfoRange(txt string) MergeinfoRange {
	var s MergeinfoRange
	s.intervals = make([]MergeinfoInterval, 0)
	for _, item := range strings.Split(txt, ",") {
		if item == "" {
			continue
		}
		var interval MergeinfoInterval
		if strings.HasSuffix(item, "*") {
			interval.NonInheritable = true
			item = strings.TrimSuffix(item, "*")
		}
		if strings.Contains(item, "-") {
			fields := strings.Split(item, "-")
			interval.Lower, _ = strconv.Atoi(fields[0])
			interval.Upper, _ = strconv.Atoi(fields[1])
		} else {
			interval.Lower, _ = strconv.Atoi(item)
			interval.Upper, _ = strconv.Atoi(item)
		}
		s.intervals = append(s.intervals, interval)
	}
	return s
}

// Optimize compacts a range as much as possible
func (s *MergeinfoRange) Optimize() {
	i := 0
	for {
		// Have we merged enough entries that we've run out of list?
		if i >= len(s.intervals)-1 {
			break
		}
		// Nope, try to merge the range at i with its right-hand neighbor
		if s.intervals[i].NonInheritable == s.intervals[i+1].NonInheritable &&
			s.intervals[i+1].Lower == s.intervals[i].Upper+1 {
			s.intervals[i].Upper = s.intervals[i+1].Upper
			s.intervals = append(s.intervals[:i+1], s.intervals[i+2:]...)
		} else {
			i++
		}
	}
}

// Stringer is a serializer as usual.
func (interval MergeinfoInterval) Stringer() string {
	out := ""
	if interval.Lower == interval.Upper {
		out = fmt.Sprintf("%d", interval.Lower)
	} else {
		out = fmt.Sprintf("%d-%d", interval.Lower, interval.Upper)
	}
	if interval.NonInheritable {
		return out + "*"
	}
	return out
}

func (s MergeinfoRange) dump() string {
	out := make([]string, 0, len(s.intervals))
	for _, interval := range s.intervals {
		out = append(out, interval.Stringer())
	}
	return strings.Join(out, ",")
}

// SetLength - alter the length field of a specified header
func SetLength(header string, data []byte, val int) []byte {
	if bytes.Contains(data, []byte(header)) {
		re := regexp.MustCompile("(" + header + "-length:) ([0-9]+)")
		return re.ReplaceAll(data, []byte("$1 "+strconv.Itoa(val)))
	} else if val > 0 {
		lf := data[len(data)-1] == '\n'
		if lf {
			data = data[:len(data)-1]
		}
		data = append(data, []byte(fmt.Sprintf("%s-length: %d\n", header, val))...)
		if lf {
			data = append(data, '\n')
		}
	}
	return data
}

// SegmentMatcher is strate for a path segment matcher
type SegmentMatcher struct {
	regexps []*regexp.Regexp
}

// NewSegmentMatcher returns a stateful object for path segment matching
func NewSegmentMatcher(patterns []string, fixed bool) SegmentMatcher {
	var s SegmentMatcher
	s.regexps = make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		if fixed {
			s.regexps[i] = regexp.MustCompile(segmentize(regexp.QuoteMeta(pattern)))
		} else {
			s.regexps[i] = regexp.MustCompile(segmentize(pattern))
		}
	}
	return s
}

func (s SegmentMatcher) pathmatch(path string) bool {
	for _, r := range s.regexps {
		if r.MatchString(path) {
			return true
		}
	}
	return false
}

// Dumpfile parsing machinery goes here

var revisionLine *regexp.Regexp = regexp.MustCompile("Revision-number: ([0-9]+)")
var textContentLength *regexp.Regexp = regexp.MustCompile("Text-content-length: ([1-9][0-9]*)")
var nodeCopyfrom *regexp.Regexp = regexp.MustCompile("Node-copyfrom-rev: ([1-9][0-9]*)")

// DumpfileSource - this class knows about Subversion dumpfile format.
type DumpfileSource struct {
	Lbs              LineBufferedSource
	Baton            *Baton
	Revision         int
	Index            int // 1-origin within nodes
	NodePath         string
	NodeProps        Properties
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

func (ds *DumpfileSource) say(text []byte) {
	matches := revisionLine.FindSubmatch(text)
	if len(matches) > 1 {
		ds.EmittedRevisions[string(matches[1])] = true
	}
	os.Stdout.Write(text)
}

// where - format reference to current node for error logging and see().
func (ds *DumpfileSource) where() string {
	return fmt.Sprintf("%d.%d", ds.Revision, ds.Index)
}

// patchMergeinfo fixes the input mergeinfo range to contain only emitted revisions
func (ds *DumpfileSource) patchMergeinfo(revrange string) string {
	inspan := parseMergeinfoRange(revrange)
	outspan := parseMergeinfoRange("")
	for _, interval := range inspan.intervals {
		for rev := interval.Lower; rev <= interval.Upper; rev++ {
			if ds.EmittedRevisions[fmt.Sprintf("%d", rev)] {
				outspan.intervals = append(outspan.intervals, MergeinfoInterval{rev, rev, interval.NonInheritable})
			}
		}
	}
	outspan.Optimize()
	return outspan.dump()
}

// Report - simpler reporting of a filtered portion of content.
func (ds *DumpfileSource) Report(
	revhook func(header StreamSection) []byte,
	prophook func(properties *Properties),
	headerhook func(header StreamSection) []byte,
	contenthook func(header []byte) []byte) {

	// The revhook is called once on every revision and can be used
	// to modify the Revision-number line.
	//
	// The prophook is called before the headerhook. It is called on
	// every property section, both per-node and per-revision.
	// When called on the revision properties the value of
	// ds.Index is zero, and will therefore match a range element
	// with an unspecified node part.
	//
	// headerhook is called on each node headers.  If this hook
	// returns nil, discarding the header, its properties and
	// content are also discarded.
	//
	// contenthook is called on the content to mutate it.
	//
	// A nil hook rgument means the section should be passed
	// through unaltered.
	//
	// All hooks can count on the DumpfileSource members to be up to
	// date, including NodePath and Revision and Index, because those.
	// are acquired before the properties or node content are parsed.

	var passthrough bool
	prestash := []byte{}
	for {
		line := ds.Lbs.Readline()
		if len(line) == 0 {
			break
		} else if strings.HasPrefix(string(line), "Revision-number:") {
			if revhook != nil {
				line = revhook(StreamSection(line))
			}
			ds.Lbs.Push(line)
			break
		}
		prestash = append(prestash, line...)
	}
	passthrough = true
	if headerhook == nil {
		ds.say(prestash)
	} else {
		// Tricky bit: to be sure of passing through inter-node
		// whitespace when 0 is noy selected, have the headerhook
		// pass back []byte{} rather than nil.
		out := headerhook(prestash)
		if out == nil {
			passthrough = false
		}
		ds.say(out)
	}
	if debug >= debugLOGIC {
		fmt.Fprintf(os.Stderr,
			"<r%s: passthrough = %v>\n",
			ds.where(), passthrough)
	}

	if !ds.Lbs.HasLineBuffered() {
		return
	}

	for {
		// Invariant: We're always looking at the beginning of a revision here
		stash := ds.Require("Revision-number:")
		ds.Index = 0
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
		stash = append(stash, ds.Require("Prop-content-length:")...)
		stash = append(stash, ds.Require("Content-length:")...)
		stash = append(stash, ds.Require(linesep)...)

		// Process per-revision properties
		props := NewProperties(ds)
		if prophook != nil {
			prophook(&props)
			proplen := len(props.Stringer())
			stash = SetLength("Prop-content", stash, proplen)
			stash = SetLength("Content", stash, proplen)
		}
		stash = append(stash, []byte(props.Stringer())...)

		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<after properties: %d>\n", ds.Lbs.linenumber)
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

		if debug >= debugPARSE {
			fmt.Fprintf(os.Stderr, "<at start of node content %d>\n", ds.Revision)
		}
		emit := true
		for {
			line := ds.Lbs.Readline()
			if len(line) == 0 {
				return
			}
			if string(line) == linesep {
				if passthrough && emit {
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<passthrough dump: %q>\n", line)
					}
					os.Stdout.Write(line)
				}
				continue
			}
			if strings.HasPrefix(string(line), "Revision-number:") {
				// Putting this check here rather than at the top of the look
				// guarantees it won't firte on revision 0
				if revhook != nil {
					line = revhook(StreamSection(line))
				}
				ds.Lbs.Push(line)
				if len(stash) != 0 && ds.Index == 0 {
					if passthrough {
						if debug >= debugPARSE {
							fmt.Fprintf(os.Stderr, "<revision stash dump: %q>\n", stash)
						}
						ds.say(stash)
					}
				}
				ds.Index = 0
				break
			}
			if strings.HasPrefix(string(line), "Node-") {
				if strings.HasPrefix(string(line), "Node-path: ") {
					ds.Index++
					ds.NodePath = string(line[11 : len(line)-1])
				}
				ds.Lbs.Push(line)

				if debug >= debugPARSE {
					fmt.Fprintf(os.Stderr, "<READ NODE BEGINS>\n")
				}
				rawHeader := ds.Require("Node-")
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
							rawHeader = append(rawHeader, line...)
							rawHeader = append(rawHeader, ds.Require("Node-copyfrom-path")...)
							continue
						}
					}
					rawHeader = append(rawHeader, line...)
					if string(line) == linesep {
						break
					}
				}
				if bytes.Contains(rawHeader, []byte("Prop-content-length")) {
					ds.NodeProps = NewProperties(ds)
				}
				// Using a read() here allows us to handle binary content
				content := []byte{}
				cl := textContentLength.FindSubmatch(rawHeader)
				if len(cl) > 1 {
					n, _ := strconv.Atoi(string(cl[1]))
					content = append(content, ds.Lbs.Read(n)...)
				}
				if debug >= debugPARSE {
					fmt.Fprintf(os.Stderr, "<READ NODE ENDS>\n")
				}

				header := StreamSection(rawHeader)
				if p := header.payload("Node-kind"); p != nil {
					ds.DirTracking[string(header.payload("Node-path"))] = bytes.Equal(p, []byte("dir"))
				}

				if debug >= debugPARSE {
					fmt.Fprintf(os.Stderr, "<header before hooks: %q>\n", header)
					fmt.Fprintf(os.Stderr, "<properties before hooks: %q>\n", ds.NodeProps)
					fmt.Fprintf(os.Stderr, "<content before hooks: %q>\n", content)
				}

				// Per-node properties come after the header.  It might be easier to
				// grok the behavior of this code if the header hook fired before the
				// per-node property hook, in the same order as those sections are read.
				// Unfortunately, the propdel and propclean header hooks need access to
				// the post-property-hook properties in order to know if they have been
				// emptied.

				properties := ""
				if bytes.Contains(header, []byte("Prop-content-length")) {
					if prophook != nil {
						prophook(&ds.NodeProps)
					}
					properties = ds.NodeProps.Stringer()
					if prophook != nil {
						header = header.setLength("Prop-content", len(properties))
						header = header.setLength("Content", len(properties)+len(content))
					}
				}

				if headerhook != nil {
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<r%s: headerhook called>\n",
							ds.where())
					}
					header = headerhook(StreamSection(header))
				}
				// header can be non-nil but empty following a wildvard expansion
				// that didn't turn up any matches.
				if len(header) == 0 {
					emit = false
				} else {
					if contenthook != nil {
						if debug >= debugPARSE {
							fmt.Fprintf(os.Stderr, "<r%s: contenthook called with>\n",
								ds.where())
						}
						newcontent := contenthook(content)
						if string(content) != string(newcontent) {
							header = header.stripChecksums()
							header = header.setLength("Text-content", len(newcontent))
							header = header.setLength("Content", len(properties)+len(newcontent))
						}
						content = newcontent
					}

					nodetxt := append(header, append([]byte(properties), content...)...)
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<nodetxt: %q>\n", nodetxt)
					}
					emit = len(nodetxt) > 0
					if emit {
						if len(stash) > 0 {
							if debug >= debugPARSE {
								fmt.Fprintf(os.Stderr, "<appending to: %q>\n", stash)
							}
							nodetxt = append(stash, nodetxt...)
							stash = []byte{}
						}
						if debug >= debugPARSE {
							fmt.Fprintf(os.Stderr, "<node dump: %q>\n", nodetxt)
						}
						ds.say(nodetxt)
					}
				}
				continue
			}
			croak("at <%d>, line %d: parse of %q doesn't look right, aborting!", ds.Revision, ds.Lbs.linenumber, string(line))
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
					if restrict == nil || restrict.ContainsRevision(rev) {
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
	end := bytes.Index(ss[offs:], []byte(linesep))
	return ss[offs : offs+end]
}

// Mutate a specified header through a hook
func (ss *StreamSection) replaceHook(htype string, hook func(string, []byte) []byte) (StreamSection, []byte, []byte) {
	header := []byte(*ss)
	offs := bytes.Index(header, []byte(htype+": "))
	if offs == -1 {
		return StreamSection(header), nil, nil
	}
	offs += len(htype) + 2
	endoffs := offs + bytes.Index(header[offs:], []byte(linesep))
	before := header[:offs]
	pathline := header[offs:endoffs]
	dup := string(pathline)
	after := make([]byte, len(header)-endoffs)
	copy(after, header[endoffs:])
	newpathline := hook(htype, pathline)
	header = before
	header = append(header, newpathline...)
	header = append(header, after...)
	return StreamSection(header), newpathline, []byte(dup)
}

// Find the index of the content of a specified field
func (ss StreamSection) index(field string) int {
	return bytes.Index([]byte(ss), []byte(field))
}

// Is this a directory node?
func (ss StreamSection) isDir(context DumpfileSource) bool {
	// Subversion sometimes omits the type field on directory operations.
	// This means we need to look back at the type of the directory's last
	// add or change operation.
	if ss.index("Node-kind") == -1 {
		return context.DirTracking[string(ss.payload("Node-path"))]
	}
	return bytes.Equal(ss.payload("Node-kind"), []byte("dir"))
}

// SetLength - alter the length field of a specified header
func (ss StreamSection) setLength(header string, val int) []byte {
	return SetLength(header, ss, val)
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

// delete - method telete a specified header
func (ss StreamSection) delete(htype string) StreamSection {
	offs := ss.index(htype)
	if offs == -1 {
		return ss
	}
	header := []byte(ss)
	header = append(header[:offs], header[offs+bytes.Index(header[offs:], []byte(linesep))+1:]...)
	return StreamSection(header)
}

func (ss StreamSection) clone() StreamSection {
	tmp := make([]byte, len(ss))
	copy(tmp, ss)
	return tmp
}

func (ss StreamSection) hasProperties() bool {
	proplen := ss.payload("Prop-content-length")
	return proplen != nil && string(proplen) != "10"
}

func (ss StreamSection) hasContent() bool {
	return ss.payload("Text-content-length") != nil
}

// Subcommand implementations begin here

// Helpers

func doSelect(source DumpfileSource, selection SubversionRange, invert bool) {
	if debug >= debugPARSE {
		fmt.Fprintf(os.Stderr, "<entering select>")
	}
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			return path, source.patchMergeinfo(revrange)
		})
	}
	headerhook := func(header StreamSection) []byte {
		if selected := selection.ContainsNode(source.Revision, source.Index) != invert; selected {
			return []byte(header)
		} else if source.Revision == 0 && !selected {
			// Nasty hack to tell it to pass through newlines
			return []byte{}
		}
		return nil
	}

	source.Report(nil, prophook, headerhook, nil)
}

// Hack paths by applying a specified transformation.
func mutatePaths(source DumpfileSource, selection SubversionRange, pathMutator func(string, []byte) []byte, nameMutator func(string) string, contentMutator func([]byte) []byte) {
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			return string(pathMutator("Mergeinfo", []byte(path))), revrange
		})
		if selection.ContainsNode(source.Revision, source.Index) {
			if userid, present := props.properties["svn:author"]; present && nameMutator != nil {
				props.properties["svn:author"] = nameMutator(userid)
			}
		}
	}
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return []byte(header)
		}
		for _, htype := range []string{"Node-path", "Node-copyfrom-path"} {
			header, _, _ = header.replaceHook(htype, pathMutator)
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, contentMutator)
}

func segmentize(pattern string) string {
	if pattern[0] == '^' && pattern[len(pattern)-1] == '$' {
		return pattern
	} else if pattern[0] == '^' {
		return pattern + "(?P<end>/|$)"
	} else if pattern[len(pattern)-1] == '$' {
		return "(?P<start>^|/)" + pattern
	}
	return "(?P<start>^|/)" + pattern + "(?P<end>/|$)"
}

// The commands proper

func closure(source DumpfileSource, selection SubversionRange, paths []string) {
	copiesFrom := make(map[string][]string)
	headerhook := func(header StreamSection) []byte {
		if selection.ContainsNode(source.Revision, source.Index) && source.NodePath != "" {
			copysource := header.payload("Node-copyfrom-path")
			if copysource != nil {
				copiesFrom[source.NodePath] = append(copiesFrom[source.NodePath], string(copysource))
			}
		}
		return nil
	}
	source.Report(nil, nil, headerhook, nil)
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

// Drop or retain ops defined by a revision selection and a path regexp.
func expungesift(source DumpfileSource, selection SubversionRange, expunge bool, fixed bool, patterns []string) {
	matcher := NewSegmentMatcher(patterns, fixed)
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return []byte(header)
		}
		matched := !expunge
		for _, hd := range []string{"Node-path", "Node-copyfrom-path"} {
			nodepath := header.payload(hd)
			if debug >= debugLOGIC {
				fmt.Fprintf(os.Stderr, "<%s: %s is %q>\n", source.where(), hd, nodepath)
			}
			if nodepath != nil {
				if expunge {
					matched = matched || matcher.pathmatch(string(nodepath))
				} else {
					matched = matched && matcher.pathmatch(string(nodepath))
				}
			}
		}
		if matched != expunge {
			return []byte(header)
		}
		return nil
	}
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			if matcher.pathmatch(path) == expunge {
				return "", ""
			}
			revrange = source.patchMergeinfo(revrange)
			return path, revrange
		})
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Replace file copy operations with explicit add/change operation
func filecopy(source DumpfileSource, selection SubversionRange, byBasename bool, matchpaths []string) {
	type trackCopy struct {
		revision int
		content  []byte
	}
	values := make(map[string][]trackCopy)
	var replacement []byte
	var nodePath string
	headerhook := func(header StreamSection) []byte {
		nodePath = source.NodePath
		if debug >= debugLOGIC {
			fmt.Fprintf(os.Stderr,
				"<r%s: filecopy investigates this revision>\n",
				source.where())
		}
		if _, ok := values[nodePath]; !ok {
			values[nodePath] = make([]trackCopy, 0)
		}
		if byBasename {
			if len(matchpaths) > 0 {
				nodePath = matchpaths[0]
			} else {
				nodePath = filepath.Base(nodePath)
			}
		}
		replacement = nil
		// The logic here is a bit more complex than might seem necessary
		// because for some inexplicable reason Subversion occasionally generates nodes
		// which have copyfrom information *and* the copy already performed - that is,
		// the node content is non-nil and should be used.  In that case we want to strip
		// out the copyfrom information without modifyinmg the content.
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return []byte(header)
		}
		if copypath := header.payload("Node-copyfrom-path"); copypath != nil {
			if byBasename {
				copypath = []byte(filepath.Base(string(copypath)))
			}
			if debug >= debugLOGIC {
				fmt.Fprintf(os.Stderr, "<r%s: filecopy investigates %s>\n",
					source.where(), copypath)
			}
			if header.hasContent() {
				header = header.delete("Node-copyfrom-path")
				header = header.delete("Node-copyfrom-rev")
				header = header.stripChecksums()
			} else {
				copyrev, _ := strconv.Atoi(string(header.payload("Node-copyfrom-rev")))
				if sources, ok := values[string(copypath)]; ok {
					for i := len(sources) - 1; i >= 0; i-- {
						if sources[i].revision <= copyrev {
							header = header.delete("Node-copyfrom-path")
							header = header.delete("Node-copyfrom-rev")
							header = header.stripChecksums()
							replacement = sources[i].content
							if debug >= debugLOGIC {
								fmt.Fprintf(os.Stderr, "    <r%s replacement is '%q'>\n", source.where(), replacement)
							}
							break
						}
					}
				} else if debug >= debugLOGIC {
					fmt.Fprintf(os.Stderr, "  <no path match found>\n")
				}
			}
		}
		return []byte(header)
	}
	contenthook := func(content []byte) []byte {
		if replacement != nil {
			content = replacement
			if debug >= debugLOGIC {
				fmt.Fprintf(os.Stderr, "  <r%s replacing with %q>\n", source.where(), content)
			}
		}
		if content != nil && len(content) > 0 {
			trampoline := values[nodePath]
			trampoline = append(trampoline, trackCopy{source.Revision, content})
			values[nodePath] = trampoline
			if debug >= debugLOGIC {
				fmt.Fprintf(os.Stderr, "<r%s: for %s, stashed content %q>\n",
					source.where(), nodePath, content)
			}
		}
		return content
	}

	source.Report(nil, nil, headerhook, contenthook)
}

// Extract log entries
func log(source DumpfileSource, selection SubversionRange) {
	SVNTimeParse := func(rdate string) time.Time {
		// Parse a date in the Subversion variant of RFC3339 format
		// An example date in SVN format is '2011-11-30T16:40:02.180831Z'
		date, ok := time.Parse(time.RFC3339Nano, rdate)
		if ok != nil {
			fmt.Fprintf(os.Stderr, "ill-formed date '%s': %v\n", rdate, ok)
			os.Exit(1)
		}
		return date
	}

	prophook := func(prop *Properties) {
		if selection.ContainsRevision(source.Revision) {
			// This test implicitly excludes r0 metadata from being dumped.
			// It is not certain this is the right thing.
			if logentry := prop.properties["svn:log"]; logentry != "" {
				os.Stdout.Write([]byte(delim + "\n"))
				author := prop.getAuthor()
				date := SVNTimeParse(prop.properties["svn:date"])
				drep := date.Format("2006-01-02 15:04:05 +0000 (Mon, 02 Jan 2006)")
				fmt.Printf("r%d | %s | %s | %d lines\n",
					source.Revision,
					author,
					drep,
					strings.Count(logentry, "\n"))
				os.Stdout.WriteString("\n" + logentry + "\n")
			}
		}
	}
	headerhook := func(header StreamSection) []byte { return nil }
	source.Report(nil, prophook, headerhook, nil)
}

// Hack pathnames to obscure them.
func obscure(seq NameSequence, source DumpfileSource, selection SubversionRange) {
	pathMutator := func(hd string, s []byte) []byte {
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
			t := pathMutator("Content", s[5:])
			c := min(len(s)-5, len(t))
			for i := 0; i < c; i++ {
				s[5+i] = t[i]
			}
		}
		return s
	}

	mutatePaths(source, selection, pathMutator, nameMutator, contentMutator)
}

func pathlist(source DumpfileSource, selection SubversionRange) {
	pathList := newOrderedStringSet()
	headerhook := func(header StreamSection) []byte {
		if selection.ContainsNode(source.Revision, source.Index) {
			if path := header.payload("Node-path"); path != nil {
				pathList.Add(string(path))
			}
		}
		return nil
	}
	source.Report(nil, nil, headerhook, nil)
	for _, item := range pathList.Iterate() {
		os.Stdout.WriteString(item + linesep)
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
		} else if patterns[i*2][len(patterns[i*2])-1] == '$' {
			ops = append(ops, transform{regexp.MustCompile("(?P<start>^|/)" + patterns[i*2]),
				append([]byte("${start}"), []byte(patterns[i*2+1])...)})
		} else {
			ops = append(ops, transform{regexp.MustCompile("(?P<start>^|/)" + patterns[i*2] + "(?P<end>/|$)"),
				append([]byte("${start}"), append([]byte(patterns[i*2+1]), []byte("${end}")...)...)})
		}
	}
	mutator := func(hd string, s []byte) []byte {
		for _, op := range ops {
			s = op.re.ReplaceAll(s, op.to)
		}
		return s
	}

	mutatePaths(source, selection, mutator, nil, nil)
}

// Pop the top segment off each pathname in an input dump
func pop(source DumpfileSource, fixed bool, patterns []string) {
	var matcher SegmentMatcher
	if len(patterns) > 0 {
		matcher = NewSegmentMatcher(patterns, fixed)
	}
	popSegment := func(ins string) string {
		if strings.Contains(ins, "/") {
			return ins[strings.Index(ins, "/")+1:]
		}
		return ""
	}
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			if len(patterns) == 0 || matcher.pathmatch(path) {
				path = popSegment(path)
			}
			return path, revrange
		})
	}
	headerhook := func(header StreamSection) []byte {
		for _, htype := range []string{"Node-path", "Node-copyfrom-path"} {
			header, _, _ = header.replaceHook(htype, func(hd string, in []byte) []byte {
				if len(patterns) == 0 || matcher.pathmatch(string(in)) {
					return []byte(popSegment(string(in)))
				}
				return in
			})
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, nil)
}

// propdel - Delete properties
func propdel(source DumpfileSource, propnames []string, selection SubversionRange) {
	var propsNuked bool
	prophook := func(props *Properties) {
		propsNuked = false
		if selection.ContainsNode(source.Revision, source.Index) {
			hadProps := props.NonEmpty()
			for _, propname := range propnames {
				props.Delete(propname)
			}
			propsNuked = hadProps && !props.NonEmpty()
		}
	}
	headerhook := func(header StreamSection) []byte {
		// Drop empty nodes left behind by propdel
		if !header.hasContent() && propsNuked && bytes.Equal(header.payload("Node-action"), []byte("change")) {
			return nil
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Set properties.
func propset(source DumpfileSource, propnames []string, selection SubversionRange) {
	prophook := func(props *Properties) {
		if selection.ContainsNode(source.Revision, source.Index) {
			for _, propname := range propnames {
				fields := strings.Split(propname, "=")
				if _, present := props.properties[fields[0]]; !present {
					props.propkeys = append(props.propkeys, fields[0])
				}
				props.properties[fields[0]] = fields[1]
			}
		}
	}
	source.Report(nil, prophook, nil, nil)
}

// Turn off property by suffix, defaulting to svn:executable
func propclean(source DumpfileSource, property string, suffixes []string, selection SubversionRange) {
	var propsNuked bool
	prophook := func(props *Properties) {
		propsNuked = false
		if selection.ContainsNode(source.Revision, source.Index) {
			hadProps := props.NonEmpty()
			for _, suffix := range suffixes {
				if strings.HasSuffix(source.NodePath, suffix) {
					props.Delete(property)
					break
				}
			}
			propsNuked = hadProps && !props.NonEmpty()
		}
	}
	headerhook := func(header StreamSection) []byte {
		// Drop empty nodes left behind by propdel
		if !header.hasContent() && propsNuked && bytes.Equal(header.payload("Node-action"), []byte("change")) {
			return nil
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Rename properties.
func proprename(source DumpfileSource, propnames []string, selection SubversionRange) {
	prophook := func(props *Properties) {
		if selection.ContainsNode(source.Revision, source.Index) {
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
	}
	source.Report(nil, prophook, nil, nil)
}

// Push a prefix segment onto each pathname in an input dump
func push(source DumpfileSource, segment string, fixed bool, patterns []string) {
	var matcher SegmentMatcher
	if len(patterns) > 0 {
		matcher = NewSegmentMatcher(patterns, fixed)
	}
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			if len(patterns) == 0 || matcher.pathmatch(path) {
				path = segment + string(os.PathSeparator) + path
			}
			return path, revrange
		})
	}
	headerhook := func(header StreamSection) []byte {
		for _, htype := range []string{"Node-path", "Node-copyfrom-path"} {
			header, _, _ = header.replaceHook(htype, func(hd string, in []byte) []byte {
				if len(patterns) == 0 || matcher.pathmatch(string(in)) {
					in = []byte(segment + string(os.PathSeparator) + string(in))
				}
				return in
			})
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Topologically reduce a dump, removing plain file modifications.
func reduce(source DumpfileSource, selection SubversionRange) {
	prophook := func(props *Properties) {
		if source.Index == 0 {
			return
		}
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			return path, source.patchMergeinfo(revrange)
		})
	}
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return []byte(header)
		}
		if string(StreamSection(header).payload("Node-kind")) == "file" && string(StreamSection(header).payload("Node-action")) == "change" && !header.hasProperties() {
			return nil
		}
		return []byte(header)
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Renumber all revisions.
func renumber(source DumpfileSource, counter int) {
	renumbering := make(map[int]int)

	renumberBack := func(n int) int {
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

	revhook := func(header StreamSection) []byte {
		newhdr, _, _ := header.replaceHook("Revision-number", func(hd string, in []byte) []byte {
			oldnum, _ := strconv.Atoi(string(in))
			newnum := counter
			counter++
			renumbering[oldnum] = newnum
			return []byte(fmt.Sprintf("%d", newnum))
		})
		return newhdr
	}

	headerhook := func(header StreamSection) []byte {
		header, _, _ = header.replaceHook("Node-copyfrom-rev", func(hd string, in []byte) []byte {
			oldnum, _ := strconv.Atoi(string(in))
			return []byte(fmt.Sprintf("%d", renumberBack(oldnum)))
		})
		return []byte(header)
	}

	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			out := ""
			digits := make([]byte, 0)
			revrange += "X"
			for i := range revrange {
				c := revrange[i]
				if bytes.ContainsAny([]byte{c}, "0123456789") {
					digits = append(digits, c)
				} else {
					if len(digits) > 0 {
						v, _ := strconv.Atoi(string(digits))
						out += fmt.Sprintf("%d", renumberBack(v))
						digits = make([]byte, 0)
					}
					// Preserve commas and other non-digit chars
					out += string(c)
				}
			}
			span := parseMergeinfoRange(out[:len(out)-1])
			span.Optimize()
			return path, span.dump()
		})
	}

	source.Report(revhook, prophook, headerhook, nil)
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

	headerhook := func(header StreamSection) []byte {
		return []byte(header)
	}
	contenthook := func(content []byte) []byte {
		return tre.ReplaceAll(content, []byte(patternParts[1]))
	}
	source.Report(nil, nil, headerhook, contenthook)
}

// Strip out ops defined by a revision selection and a path regexp.
func see(source DumpfileSource, selection SubversionRange) {
	seenode := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return nil
		}
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
		fmt.Printf("%-5s %-8s %s\n", source.where(), action, path)
		return nil
	}
	seeprops := func(properties *Properties) {
		if !selection.ContainsNode(source.Revision, source.Index) {
			return
		}
		for _, skippable := range []string{"svn:log", "svn:date", "svn:author"} {
			if _, ok := properties.properties[skippable]; ok {
				return
			}
		}
		props := properties.String()
		if props != "" {
			fmt.Printf("%-5s %-8s %s\n", source.where(), "propset", props)
		}
	}
	source.Report(nil, seeprops, seenode, nil)
}

// Set the copyfrom path
func setcopyfrom(source DumpfileSource, selection SubversionRange, newpath string) {
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) {
			return []byte(header)
		}
		if header.payload("Node-copyfrom-path") == nil {
			croak("setcopyfrom applied to a non-copy node %s", source.where())
		}
		header, _, _ = header.replaceHook("Node-copyfrom-path", func(hdr string, in []byte) []byte {
			return []byte(newpath)
		})
		return []byte(header)
	}
	source.Report(nil, nil, headerhook, nil)
}

// Select a portion of the dump file not defined by a revision selection.
func sselect(source DumpfileSource, selection SubversionRange) {
	doSelect(source, selection, false)
}

// Mutate log entries.
func setlog(source DumpfileSource, logpath string, selection SubversionRange) {
	fd, ok := os.Open(logpath)
	if ok != nil {
		croak("couldn't open " + logpath)
	}
	logpatch := NewLogfile(fd, &selection)
	prophook := func(prop *Properties) {
		if selection.ContainsRevision(source.Revision) && source.Index == 0 {
			if _, haslog := prop.properties["svn:log"]; haslog && logpatch.Contains(source.Revision) {
				logentry := logpatch.comments[source.Revision]
				if string(logentry.author) != prop.getAuthor() {
					croak("author of revision %d doesn't look right, aborting!\n", source.Revision)
				}
				prop.properties["svn:log"] = string(logentry.text)
			}
		}
	}
	source.Report(nil, prophook, nil, nil)
}

// Set the node path
func setpath(source DumpfileSource, selection SubversionRange, newpath string) {
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) {
			return []byte(header)
		}
		header, _, _ = header.replaceHook("Node-path", func(hdr string, in []byte) []byte {
			return []byte(newpath)
		})
		return []byte(header)
	}
	source.Report(nil, nil, headerhook, nil)
}

// Skip unwanted copies between specified revisions
func skipcopy(source DumpfileSource, selection SubversionRange) {
	//within := false
	var stashPath []byte
	var stashRev []byte
	headerhook := func(header StreamSection) []byte {
		if source.Revision == 0 {
			return []byte(header)
		}
		if selection.Lowerbound().Equals(SubversionEndpoint{source.Revision, source.Index}) {
			stashRev = header.payload("Node-copyfrom-rev")
			stashPath = header.payload("Node-copyfrom-path")
			if stashRev == nil || stashPath == nil {
				croak("r%s: early node of skipcopy is not a copy", source.where())
			}
			//within = true
		}
		if selection.Upperbound().Equals(SubversionEndpoint{source.Revision, source.Index}) {
			//within = false
			if header.payload("Node-copyfrom-rev") == nil || header.payload("Node-copyfrom-path") == nil {
				croak("r%s: late node of skipcopy is not a copy", source.where())
			}
			header, _, _ = header.replaceHook("Node-copyfrom-rev", func(hd string, in []byte) []byte {
				return stashRev
			})
			header, _, _ = header.replaceHook("Node-copyfrom-path", func(hd string, in []byte) []byte {
				return stashPath
			})
		}
		return []byte(header)
	}
	source.Report(nil, nil, headerhook, nil)
}

func strip(source DumpfileSource, selection SubversionRange, fixed bool, patterns []string) {
	var matcher SegmentMatcher
	if len(patterns) > 0 {
		matcher = NewSegmentMatcher(patterns, fixed)
	}
	var stripIt bool
	headerhook := func(header StreamSection) []byte {
		stripIt = source.Revision > 0 && selection.ContainsNode(source.Revision, source.Index) && (len(patterns) == 0 || matcher.pathmatch(source.NodePath))
		if stripIt {
			header = header.stripChecksums()
		}
		return []byte(header)
	}
	contenthook := func(content []byte) []byte {
		if stripIt {
			if len(content) > 0 { //len([]nil == 0)
				// Avoid replacing symlinks, a reposurgeon sanity check barfs.
				if !bytes.HasPrefix(content, []byte("link ")) {
					tell := fmt.Sprintf("Revision is %d, file path is %s.\n",
						source.Revision, source.NodePath)
					content = []byte(tell)
				}
			}
		}
		return content
	}
	source.Report(nil, nil, headerhook, contenthook)
}

// Hack paths by swapping the top two components - if "structural" is on, be Subversion-aware
// and also attempt to merge spans of partial branch creations.
func swap(source DumpfileSource, selection SubversionRange, fixed bool, patterns []string, structural bool) {
	var matcher SegmentMatcher
	if len(patterns) > 0 {
		matcher = NewSegmentMatcher(patterns, fixed)
	}
	type parsedNode struct {
		role      string
		action    []byte
		isDelete  bool
		isCopy    bool
		isDir     bool
		coalesced bool
	}
	wildcards := make(map[string]orderedStringSet)
	var wildcardKey string
	const wildcardMark = '*'
	var lastPromotedSource string
	stdlayout := func(payload []byte) bool {
		return bytes.HasPrefix(payload, []byte("trunk")) || bytes.HasPrefix(payload, []byte("tags")) || bytes.HasPrefix(payload, []byte("branches"))
	}
	// This function is called on paths to swap their project and second-level components,
	// then if necessary truncate them to promote operations on projet=local trunks/tags/branches
	// to global ones
	//
	// The swap part is tricky because in the structural case the "second component" isn't a
	// single path segment, but can be two of the form PROJECT/branches/SUBDIR or
	// PROJECT/tags/SUBDIR, The other complication is what we need to do to a
	// two-component path of the form PROJECT/trunk, PROJECT/branches, or PROJECT/tags.
	//
	// Things we know:
	//
	// 1. We need swapping on every path that is not alreadt in standard layout.
	//
	// 2. We never want to trim paths unless we're doing a structural swap.
	//
	// 3. We never want to trim paths in file operations at all.
	//
	// 4. The only paths eligible for trimming are paths that refer to a subdirectory
	// of a project-local trunk, or of one of its branches, or of one of its tags -
	// those are what we may need to promote. We don't want to modify any copies
	// or other operations deeper in the tree than that because they take place
	// *within* projects.
	//
	// 5. Delete operations should only be trimmed as part of branch-rename sequences.
	//
	// All the swap and promotion logic lives here. Paths for all operations - adds,
	// deletes, changes, and copies - go through here.
	//
	swapper := func(sourcehdr string, path []byte, parsed parsedNode) []byte {
		// mergeinfo paths are rooted - leading slash should
		// be ignored, then restored.
		rooted := len(path) > 0 && (path[0] == byte(os.PathSeparator))
		if rooted {
			path = path[1:]
		}
		originalPath := path
		parts := bytes.Split(path, []byte{os.PathSeparator})
		if len(parts) >= 2 {
			// Swapping logic
			project := string(parts[0])
			if !structural {
				parts[0] = parts[1]
				parts[1] = []byte(project)
			} else if !stdlayout(path) {
				under := string(parts[1])
				if under == "trunk" {
					// PROJECT/trunk/...;  Just map this to trunk/PROJECT/...,
					// Lossless transformation, still refers to the same
					// set of paths.
					parts[0] = parts[1]
					parts[1] = []byte(project)
				} else if under == "branches" || under == "tags" {
					// Shift "branches" or "tags" to top level
					parts[0] = []byte(under)
					if len(parts) >= 3 {
						if parsed.isDir && len(parts) == 3 {
							// Exactly three components, PROJECT/branches/SUBDIR
							// or PROJECT/tags/SUBDIR.
							//
							// This is where we capture information about what
							// branches and tags exist under a specified project
							// directory.
							key := project + string(os.PathSeparator) + under
							subbranch := string(parts[2])
							switch parsed.role {
							case "add":
								fallthrough
							case "copy":
								trackSet := wildcards[key]
								trackSet.Add(subbranch)
								wildcards[key] = trackSet
							case "delete":
								trackSet := wildcards[key]
								trackSet.Remove(subbranch)
								wildcards[key] = trackSet
							}
						}
						// Mutate to tags/SUBDIR/PROJECT/... or branches/SUBDIR/PROJECT/...
						parts[1] = parts[2]
						parts[2] = []byte(project)
					} else { // len(parts) == 2
						// Deal with paths of the form PROJECT/branches or PROJECT/tags
						// and no subdirectory following. Dangerous curve!
						//
						// If you're doing a structural swap and see a path
						// that looks like this, simply swapping the two parts
						// cannot be correct.  By the premise of this operation,
						// PROJECT should become a directory name under some branch
						// spec which isn't identified here.
						//
						// We may need to insert a wildcard for later expansion
						// at a later point in this code.
						if !parsed.isDir {
							// Probably never happens but let's be safe.
							parts[1] = []byte(project)
						} else if under != "trunk" {
							switch parsed.role {
							case "add":
								// Start tracking subbranches/subtags of PROJECT.
								wildcards[string(path)] = newOrderedStringSet()
								// Then drop this path - nothing else needs doing.
								return nil
							case "delete":
								// Stop tracking subbranches/subtags of PROJECT.
								delete(wildcards, string(path))
								// Then drop this path - nothing else needs doing.
								return nil
							case "change":
								if source.NodeProps.NonEmpty() {
									croak("r%s: unswappable copy of %s has properties",
										source.where(), path)
								}
								return nil
							case "copy":
								if sourcehdr == "Node-copyfrom-path" {
									wildcardKey = string(path)
									parts[1] = []byte{wildcardMark}
									parts = append(parts, []byte(project))
								}
							case "mergeinfo":
								croak("r$s: unexpected mergeinfo of path %s",
									source.where(), path)
							default:
								croak("r%s: unexpected action %s on path %s",
									source.where(), parsed.role, path)
							}
						}
					}
				}
			}
			if debug >= debugLOGIC {
				new := bytes.Join(parts, []byte{os.PathSeparator})
				fmt.Fprintf(os.Stderr, "<r%s: swap of %s %s %s -> %s>\n",
					source.where(), parsed.role, sourcehdr, originalPath, new)
			}
			swapped := string(bytes.Join(parts, []byte{os.PathSeparator}))
			copyable := func(parts [][]byte) bool {
				if len(parts) == 2 && string(parts[0]) == "trunk" {
					return true
				}
				if len(parts) == 3 && (string(parts[0]) == "tags" || string(parts[0]) == "branches") {
					return true
				}
				return false
			}
			if structural && !stdlayout(path) && parsed.isDir && copyable(parts) {
				var old []byte
				if debug >= debugLOGIC {
					old = bytes.Join(parts, []byte{os.PathSeparator})
				}
				if sourcehdr == "Node-path" {
					if parsed.isCopy {
						parts = parts[:len(parts)-1]
					}
					// Only branch and tag deletions should be promoted, never trunk ones.
					if parsed.isDelete && !bytes.Equal(parts[0], []byte("trunk")) {
						if debug >= debugLOGIC {
							fmt.Fprintf(os.Stderr, "<r%s: comparing %s with %s>\n",
								source.where(), swapped, lastPromotedSource)
						}
						if lastPromotedSource == swapped {
							parts = parts[:len(parts)-1]
						}
						if debug >= debugLOGIC {
							fmt.Fprintf(os.Stderr, "<r%s: from %s deleting %s>\n",
								source.where(), source.NodePath, bytes.Join(parts, []byte{os.PathSeparator}))
						}
						lastPromotedSource = ""
					}
				} else if sourcehdr == "Node-copyfrom-path" && parsed.coalesced {
					parts = parts[:len(parts)-1]
					lastPromotedSource = string(swapped)
					if debug >= debugLOGIC {
						fmt.Fprintf(os.Stderr, "<r%s: setting lastPromotedSource = %s>\n",
							source.where(), lastPromotedSource)
					}
				}
				if debug >= debugLOGIC {
					new := bytes.Join(parts, []byte{os.PathSeparator})
					fmt.Fprintf(os.Stderr, "<r%s: trim of %s %s -> %s>\n",
						source.where(), sourcehdr, old, new)
				}
			}
		}
		if rooted {
			parts[0] = append([]byte{os.PathSeparator}, parts[0]...)
		}
		return bytes.Join(parts, []byte{os.PathSeparator})
	}
	prophook := func(props *Properties) {
		props.MutateMergeinfo(func(path string, revrange string) (string, string) {
			var dummy parsedNode
			dummy.role = "mergeinfo"
			return string(swapper("", []byte(path), dummy)), revrange
		})
	}
	var oldval, newval []byte
	headerhook := func(header StreamSection) []byte {
		if !selection.ContainsNode(source.Revision, source.Index) || source.Revision == 0 {
			return []byte(header)
		}
		nodePath := header.payload("Node-path")
		var parsed parsedNode
		parsed.action = header.payload("Node-action")
		parsed.isDelete = bytes.Equal(parsed.action, []byte("delete"))
		parsed.isCopy = header.index("Node-copyfrom-path") != -1
		parsed.isDir = header.isDir(source)
		parsed.role = string(parsed.action)
		parsed.coalesced = false

		if parsed.isCopy {
			parsed.role = "copy"
		}
		// All operations, includung copies.
		if len(patterns) == 0 || matcher.pathmatch(string(nodePath)) {
			// Special handling of operations on bare project directories
			if structural && bytes.Count(nodePath, []byte{os.PathSeparator}) == 0 {
				// Top-level copies must be split
				if parsed.role == "copy" {
					if header.hasProperties() {
						croak("r%s: can't split a top node with nonempty properties.", source.where())
					}
					if header.hasContent() {
						croak("r%s: can't split a top node with nonempty content.", source.where())
					}
					if debug >= debugPARSE {
						fmt.Fprintf(os.Stderr, "<split firing on %q>\n", header)
					}
					header.delete("Prop-content-length")
					prefixer := func(header StreamSection, prefix string) []byte {
						out := header.clone()
						for _, tag := range [2]string{"Node-path", "Node-copyfrom-path"} {
							out, _, _ = out.replaceHook(tag, func(hd string, in []byte) []byte {
								return append([]byte(prefix), in...)
							})
						}
						return append(out, '\n')
					}
					os.Stdout.Write(prefixer(header, "trunk/"))
					for _, under := range [2]string{"branches", "tags"} {
						copyfrom := string(header.payload("Node-copyfrom-path"))
						key := copyfrom + string(os.PathSeparator) + under
						for _, subpart := range wildcards[key] {
							// Add to tracking set in case of future copies from here
							key := source.NodePath + string(os.PathSeparator) + string(under)
							trackSet := wildcards[key]
							trackSet.Add(subpart)
							wildcards[key] = trackSet
							os.Stdout.Write(prefixer(header, under+"/"+subpart+"/"))
						}
					}
					return nil
				}
				// Non-copy operations - pass through anything that looks like standard layout
				if !stdlayout(nodePath) {
					// Don't retain non-copy
					// operations on project
					// directories, these are
					// replaced by the creation of
					// the top-level
					// trunk/tags/branches
					// directories in the swapped
					// hierarchy.  Error out if
					// there is metadata to be
					// preserved.
					if header.hasProperties() {
						croak("properties on top-level directory %d:%s, must be removed by hand", source.Revision, nodePath)
					}
					return nil
				}
			}

			wildcardKey = ""
			header, newval, oldval = header.replaceHook("Node-path", func(hd string, path []byte) []byte {
				return swapper(hd, path, parsed)
			})
			if oldval != nil && newval == nil {
				return nil
			}
			parsed.coalesced = len(newval) < len(oldval)
			if debug >= debugLOGIC {
				fmt.Fprintf(os.Stderr, "<r%s: %q -> %q, coalesced = %v>\n", source.where(), oldval, newval, parsed.coalesced)
			}
		}
		// Copy-only logic.
		if len(patterns) == 0 || matcher.pathmatch(string(header.payload("Node-copyfrom-path"))) {
			header, newval, oldval = header.replaceHook("Node-copyfrom-path", func(hd string, path []byte) []byte {
				return swapper(hd, path, parsed)
			})
			if bytes.Contains(newval, []byte{wildcardMark}) {
				header, _, _ = header.replaceHook("Node-path", func(hd string, in []byte) []byte {
					return append(in, os.PathSeparator, wildcardMark)
				})
			}
		}

		if wildcardKey == "" {
			return []byte(header)
		}
		all := make([]byte, 0)
		for _, subbranch := range wildcards[wildcardKey].Iterate() {
			clone := StreamSection(bytes.Replace(header,
				[]byte{wildcardMark}, []byte(subbranch),
				-1))
			clone = clone.delete("Prop-content-length")
			clone = clone.delete("Content-length")
			all = append(all, []byte(clone)...)
		}
		return all
	}
	source.Report(nil, prophook, headerhook, nil)
}

// Neutralize the input test load
func testify(source DumpfileSource, counter int) {
	const NeutralUser = "fred"
	const NeutralUserLen = len(NeutralUser)
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
			line = []byte(NeutralUser + linesep)
			state = 0
		} else if state == 6 {
			t := time.Unix(int64((counter-1)*10), 0).UTC().Format(time.RFC3339)
			t2 := t[:19] + ".000000Z"
			line = []byte(t2 + linesep)
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
	var base int
	var fixed bool
	var logentries string
	var property string
	var rangestr string
	var segment string
	var infile string
	input := os.Stdin
	flag.IntVar(&base, "b", 0, "base value to renumber from")
	flag.IntVar(&base, "base", 0, "base value to renumber from")
	flag.IntVar(&debug, "d", 0, "enable debug messages")
	flag.IntVar(&debug, "debug", 0, "enable debug messages")
	flag.BoolVar(&fixed, "f", false, "disable regexp interpretation")
	flag.BoolVar(&fixed, "fixed", false, "disable regexp interpretation")
	flag.StringVar(&infile, "i", "", "set input file")
	flag.StringVar(&infile, "infile", "", "set input file")
	flag.StringVar(&logentries, "l", "", "pass in log patch")
	flag.StringVar(&logentries, "logentries", "", "pass in log patch")
	flag.StringVar(&property, "p", "svn:executable", "set property to be cleaned")
	flag.StringVar(&property, "property", "svn:executable", "set property to be cleaned")
	flag.BoolVar(&quiet, "q", false, "disable progress messages")
	flag.BoolVar(&quiet, "quiet", false, "disable progress messages")
	flag.StringVar(&rangestr, "r", "", "set selection range")
	flag.StringVar(&rangestr, "range", "", "set selection range")
	flag.StringVar(&segment, "s", "trunk", "set set segment for push operation")
	flag.StringVar(&segment, "segment", "trunk", "set set segment for push operation")
	flag.StringVar(&tag, "t", "", "set error tag")
	flag.StringVar(&tag, "tag", "", "set error tag")
	flag.Parse()

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
			baton = NewBaton(helpdict[flag.Arg(0)].oneliner, "done")
		} else {
			baton = nil
		}
	}

	assertNoArgs := func() {
		if len(flag.Args()) != 1 {
			croak("extra arguments detected after command keyword!\n")
		}
	}

	assertNoSelection := func() {
		if rangestr != "" {
			croak("subcommand does not take a selection!\n")
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
	case "docgen": // Not documented
		assertNoArgs()
		assertNoSelection()
		dumpDocs()
	case "expunge":
		expungesift(NewDumpfileSource(input, baton), selection, true, fixed, flag.Args()[1:])
	case "filecopy":
		filecopy(NewDumpfileSource(input, baton), selection, fixed, flag.Args()[1:])
	case "help":
		assertNoSelection()
		if len(flag.Args()) == 1 {
			os.Stdout.WriteString(dochead)
			keys := make([]string, 0)
			for k := range helpdict {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, key := range keys {
				os.Stdout.WriteString(fmt.Sprintf("%-12s %s\n", key, helpdict[key].oneliner))
			}
			break
		}
		if cdoc, ok := helpdict[flag.Arg(1)]; ok {
			os.Stdout.WriteString(cdoc.text)
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
		assertNoSelection()
		pop(NewDumpfileSource(input, baton), fixed, flag.Args()[1:])
	case "propclean":
		propclean(NewDumpfileSource(input, baton), property, flag.Args()[1:], selection)
	case "propdel":
		propdel(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "propset":
		propset(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "proprename":
		proprename(NewDumpfileSource(input, baton), flag.Args()[1:], selection)
	case "reduce":
		assertNoArgs()
		reduce(NewDumpfileSource(input, baton), selection)
	case "push":
		assertNoSelection()
		push(NewDumpfileSource(input, baton), segment, fixed, flag.Args()[1:])
	case "renumber":
		assertNoArgs()
		assertNoSelection()
		renumber(NewDumpfileSource(input, baton), base)
	case "replace":
		replace(NewDumpfileSource(input, baton), selection, flag.Args()[1])
	case "see":
		assertNoArgs()
		see(NewDumpfileSource(input, baton), selection)
	case "select":
		assertNoArgs()
		sselect(NewDumpfileSource(input, baton), selection)
	case "setcopyfrom":
		setcopyfrom(NewDumpfileSource(input, baton), selection, flag.Args()[1])
	case "setlog":
		if logentries == "" {
			fmt.Fprintf(os.Stderr, "repocutter: setlog requires a log entries file.\n")
			os.Exit(1)
		}
		setlog(NewDumpfileSource(input, baton), logentries, selection)
	case "setpath":
		setpath(NewDumpfileSource(input, baton), selection, flag.Args()[1])
	case "sift":
		expungesift(NewDumpfileSource(input, baton), selection, false, fixed, flag.Args()[1:])
	case "skipcopy":
		skipcopy(NewDumpfileSource(input, baton), selection)
	case "strip":
		strip(NewDumpfileSource(input, baton), selection, fixed, flag.Args()[1:])
	case "swap":
		swap(NewDumpfileSource(input, baton), selection, fixed, flag.Args()[1:], false)
	case "swapsvn":
		swap(NewDumpfileSource(input, baton), selection, fixed, flag.Args()[1:], true)
	case "testify":
		assertNoArgs()
		assertNoSelection()
		testify(NewDumpfileSource(input, baton), base)
	case "version":
		assertNoArgs()
		assertNoSelection()
		fmt.Println(version)
	default:
		croak("%q: unknown subcommand", flag.Arg(0))
	}
	if baton != nil {
		baton.End("")
	}
}
