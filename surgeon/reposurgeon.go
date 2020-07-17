// reposurgeon is an editor/converter for version-control histories.
package main

// This code is intended to be hackable to support for special-purpose or
// custom operations, though it's even better if you can come up with a new
// surgical primitive general enough to ship with the stock version.  For
// either case, here's a guide to the architecture.
//
// The core classes are largely about deserializing and reserializing
// import streams.  In between these two operations the repo state
// lives in a fairly simple object, Repository. The main part of
// Repository is just a list of objects implementing the Event
// interface - Commits, Blobs, Tags, Resets, and Passthroughs.
// These are straightforward representations of the command types in
// an import stream, with Passthrough as a way of losslessly conveying
// lines the parser does not recognize.
//
//  +-------------+    +---------+    +-------------+
//  | Deserialize |--->| Operate |--->| Reserialize |
//  +-------------+    +---------+    +-------------+
//
// The general theory of reposurgeon is: you deserialize, you do stuff
// to the event list that preserves correctness invariants, you
// reserialize.  The "do stuff" is mostly not in the core classes, but
// there is one major exception.  The primitive to delete a commit and
// squash its fileops forwards or backwards is seriously intertwined
// with the core classes and actually makes up almost 50% of Repository
// by line count.
//
// The rest of the surgical code lives outside the core classes. Most
// of it lives in the RepoSurgeon class (the command interpreter) or
// the RepositoryList class (which encapsulated by-name access to a list
// of repositories and also hosts surgical operations involving
// multiple repositories). A few bits, like the repository reader and
// builder, have enough logic that's independent of these
// classes to be factored out of it.
//
// In designing new commands for the interpreter, try hard to keep them
// orthogonal to the selection-set code. As often as possible, commands
// should all have a similar form with a (single) selection set argument.
//
// VCS is not a core class.  The code for manipulating actual repos is bolted
// on the the ends of the pipeline, like this:
//
//  +--------+    +-------------+    +---------+    +-----------+    +--------+
//  | Import |--->| Deserialize |--->| Operate |--->| Serialize |--->| Export |
//  +--------+    +-------------+ A  +---------+    +-----------+    +--------+
//       +-----------+            |
//       | Extractor |------------+
//       +-----------+
//
// The Import and Export boxes call methods in VCS.
//
// Extractor classes build the deserialized internal representation directly.
// Each extractor class is a set of VCS-specific methods to be used by the
// RepoStreamer driver class.  Key detail: when a repository is recognized by
// an extractor it sets the repository type to point to the corresponding
// VCS instance.

// This code was translated from Python. It retains, for internal
// documentation purposes, the Python convention of using leading
// underscores on field names to flag fields that should never be
// referenced outside a method of the associated struct.
//
// The capitalization of other fieldnames looks inconsistent because
// the code tries to retain the lowercase Python names and
// compartmentalize as much as possible to be visible only within the
// declaring package.  Some fields are capitalized for backwards
// compatibility with the setfield command in the Python
// implementation, others (like some members of FileOp) because
// there's an internal requirement that they be settable by the Go
// reflection primitives.
//
// Copyright by Eric S. Raymond
// SPDX-License-Identifier: BSD-2-Clause

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unsafe" // Actually safe - only uses Sizeof

	shlex "github.com/anmitsu/go-shlex"
	difflib "github.com/ianbruene/go-difflib/difflib"
	kommandant "gitlab.com/ianbruene/kommandant"
	terminal "golang.org/x/crypto/ssh/terminal"
	ianaindex "golang.org/x/text/encoding/ianaindex"
)

// Control is global context. Used to be named Context until its global
// collided with the Go context package.
type Control struct {
	innerControl
	logmask    uint
	logfp      io.Writer
	baton      *Baton
	logcounter int
	signals    chan os.Signal
	logmutex   sync.Mutex
	// The abort flag
	abortScript    bool
	abortLock      sync.Mutex
	flagOptions    map[string]bool
	listOptions    map[string]orderedStringSet
	mapOptions     map[string]map[string]string
	branchMappings []branchMapping
	readLimit      uint64
	profileNames   map[string]string
	startTime      time.Time
	lineSep        string
}

type branchMapping struct {
	match   *regexp.Regexp
	replace string
}

func (b branchMapping) String() string {
	return fmt.Sprintf("{match=%s, replace=%s}", b.match, b.replace)
}

func (ctx *Control) isInteractive() bool {
	return ctx.flagOptions["interactive"]
}

func (ctx *Control) init() {
	ctx.flagOptions = make(map[string]bool)
	ctx.listOptions = make(map[string]orderedStringSet)
	ctx.mapOptions = make(map[string]map[string]string)
	ctx.signals = make(chan os.Signal, 1)
	ctx.logmask = (logWARN << 1) - 1
	batonLogFunc := func(s string) {
		// it took me about an hour to realize that the
		// percent sign inside s was breaking this
		if logEnable(logBATON) {
			logit("%s", s)
		}
	}
	baton := newBaton(control.isInteractive(), batonLogFunc)
	var b interface{} = baton
	ctx.logfp = b.(io.Writer)
	ctx.baton = baton
	signal.Notify(control.signals, os.Interrupt)
	go func() {
		for {
			<-control.signals
			control.setAbort(true)
			respond("Interrupt\n")
		}
	}()
	ctx.startTime = time.Now()
	control.lineSep = "\n"
}

var control Control

func (ctx *Control) getAbort() bool {
	ctx.abortLock.Lock()
	defer ctx.abortLock.Unlock()
	return ctx.abortScript
}

func (ctx *Control) setAbort(cond bool) {
	ctx.abortLock.Lock()
	defer ctx.abortLock.Unlock()
	ctx.abortScript = cond
}

/*
 * Logging and responding
 */

// logEnable is a hook to set up log-message filtering.
func logEnable(logbits uint) bool {
	return (control.logmask & logbits) != 0
}

// nuke removes a (large) directory
func nuke(directory string, legend string) {
	if exists(directory) {
		if !control.flagOptions["quiet"] {
			//control.baton.startProcess(legend, "")
			//defer control.baton.endProcess()
		}
		os.RemoveAll(directory)
	}
}

func croak(msg string, args ...interface{}) {
	content := fmt.Sprintf(msg, args...)
	control.baton.printLogString("reposurgeon: " + content + control.lineSep)
	if !control.flagOptions["relax"] {
		control.setAbort(true)
	}
}

func logit(msg string, args ...interface{}) {
	var leader string
	content := fmt.Sprintf(msg, args...)
	if _, ok := control.logfp.(*os.File); ok {
		leader = rfc3339(time.Now())
	} else {
		leader = "reposurgeon"
	}
	control.logmutex.Lock()
	control.logfp.Write([]byte(leader + ": " + content + control.lineSep))
	control.logcounter++
	control.logmutex.Unlock()
}

// respond is to be used for console messages that shouldn't be logged
func respond(msg string, args ...interface{}) {
	if control.isInteractive() {
		content := fmt.Sprintf(msg, args...)
		control.baton.printLogString("reposurgeon: " + content + control.lineSep)
	}
}

// screenwidth returns the current width of the terminal window.
func screenwidth() int {
	width := 80
	if !control.flagOptions["testmode"] && terminal.IsTerminal(0) {
		var err error
		width, _, err = terminal.GetSize(0)
		if err != nil {
			log.Fatal(err)
		}
	}
	return width
}

// LineParse is state for a simple CLI parser with options and redirects.
type LineParse struct {
	repolist     *RepositoryList
	line         string
	capabilities orderedStringSet
	stdin        io.ReadCloser
	stdout       io.WriteCloser
	infile       string
	outfile      string
	redirected   bool
	options      orderedStringSet
	closem       []io.Closer
}

func (rl *RepositoryList) newLineParse(line string, capabilities orderedStringSet) *LineParse {
	caps := make(map[string]bool)
	for _, cap := range capabilities {
		caps[cap] = true
	}
	lp := LineParse{
		line:         line,
		capabilities: capabilities,
		stdin:        os.Stdin,
		stdout:       control.baton,
		redirected:   false,
		options:      make([]string, 0),
		closem:       make([]io.Closer, 0),
	}

	var err error
	// Input redirection
	match := regexp.MustCompile("<[^ ]+").FindStringIndex(lp.line)
	if match != nil {
		if !caps["stdin"] {
			panic(throw("command", "no support for < redirection"))
		}
		lp.infile = lp.line[match[0]+1 : match[1]]
		if lp.infile != "" && lp.infile != "-" {
			lp.stdin, err = os.Open(lp.infile)
			if err != nil {
				panic(throw("command", "can't open %s for read", lp.infile))
			}
			lp.closem = append(lp.closem, lp.stdin)
		}
		lp.line = lp.line[:match[0]] + lp.line[match[1]:]
		lp.redirected = true
	}
	// Output redirection
	match = regexp.MustCompile("(>>?)([^ ]+)").FindStringSubmatchIndex(lp.line)
	if match != nil {
		if !caps["stdout"] {
			panic(throw("command", "no support for > redirection"))
		}
		lp.outfile = lp.line[match[2*2+0]:match[2*2+1]]
		if lp.outfile != "" && lp.outfile != "-" {
			info, err := os.Stat(lp.outfile)
			if err == nil {
				if info.Mode().IsDir() {
					panic(throw("command", "can't redirect output to %s, which is a directory", lp.outfile))
				}
			}
			// flush the outfile, if it happens to be a file
			// that Reposurgeon has already opened
			mode := os.O_WRONLY
			if match[2*1+1]-match[2*1+0] > 1 {
				mode |= os.O_CREATE | os.O_APPEND
			} else {
				mode |= os.O_CREATE
				// Unix delete doesn't nuke a file
				// immediately, it (a) removes the
				// directory reference, and (b)
				// schedules the file for actual
				// deletion on it when the last file
				// descriptor open to it is closed.
				// Thus, by deleting the file if it
				// already exists we ennsure that any
				// seekstreams pointing to it will
				// continue to get valid data.
				os.Remove(lp.outfile)
			}
			lp.stdout, err = os.OpenFile(lp.outfile, mode, userReadWriteMode)
			if err != nil {
				panic(throw("command", "can't open %s for writing", lp.outfile))
			}
			lp.closem = append(lp.closem, lp.stdout)
		}
		lp.line = lp.line[:match[2*0+0]] + lp.line[match[2*0+1]:]
		lp.redirected = true
	}
	// Options
	for true {
		match := regexp.MustCompile("--([^ ]+)").FindStringSubmatchIndex(lp.line)
		if match == nil {
			break
		} else {
			lp.options = append(lp.options, lp.line[match[2]-2:match[3]])
			lp.line = lp.line[:match[2]-2] + lp.line[match[3]:]
		}
	}
	// strip excess whitespace
	lp.line = strings.TrimSpace(lp.line)
	// Dash redirection
	if !lp.redirected && lp.line == "-" {
		if !caps["stdout"] && !caps["stdin"] {
			panic(throw("command", "no support for - redirection"))
		} else {
			lp.line = ""
			lp.redirected = true
		}
	}
	return &lp
}

// Tokens returns the argument token list after the parse for redirects.
func (lp *LineParse) Tokens() []string {
	return strings.Fields(lp.line)
}

// OptVal looks for an option flag on the line, returns value and presence
func (lp *LineParse) OptVal(opt string) (val string, present bool) {
	for _, option := range lp.options {
		if strings.Contains(option, "=") {
			parts := strings.SplitN(option, "=", 3)
			if len(parts) > 1 && parts[0] == opt {
				return parts[1], true
			}
			return "", true

		} else if option == opt {
			return "", true
		}
	}
	return "", false
}

// RedirectInput connects standard input to the specified reader
func (lp *LineParse) RedirectInput(reader io.Closer) {
	if fp, ok := lp.stdin.(io.Closer); ok {
		for i, f := range lp.closem {
			if f == fp {
				lp.closem[i] = fp
				return
			}
		}
		lp.closem = append(lp.closem, reader)
	}
}

// Closem ckoses all redirects associated with this command
func (lp *LineParse) Closem() {
	for _, f := range lp.closem {
		if f != nil {
			f.Close()
		}
	}
}

// respond is to be used for console messages that shouldn't be logged
func (lp *LineParse) respond(msg string, args ...interface{}) {
	content := fmt.Sprintf(msg, args...)
	control.baton.printLogString(content + control.lineSep)
}

// Reposurgeon tells Kommandant what our local commands are
type Reposurgeon struct {
	cmd          *kommandant.Kmdt
	definitions  map[string][]string
	inputIsStdin bool
	RepositoryList
	SelectionParser
	callstack    [][]string
	selection    orderedIntSet
	history      []string
	preferred    *VCS
	extractor    Extractor
	startTime    time.Time
	logHighwater int
	ignorename   string
}

var unclean = regexp.MustCompile("^[^\n]*\n[^\n]")

func newReposurgeon() *Reposurgeon {
	rs := new(Reposurgeon)
	rs.SelectionParser.subclass = rs
	rs.startTime = time.Now()
	rs.definitions = make(map[string][]string)
	rs.inputIsStdin = true
	// These are globals and should probably be set in init().
	for _, option := range optionFlags {
		control.listOptions[option[0]] = newOrderedStringSet()
	}
	control.listOptions["svn_branchify"] = orderedStringSet{"trunk", "tags/*", "branches/*", "*"}
	return rs
}

// SetCore is a Kommandant housekeeping hook.
func (rs *Reposurgeon) SetCore(k *kommandant.Kmdt) {
	rs.cmd = k
	k.OneCmdHook = func(ctx context.Context, line string) (stop bool) {
		defer func(stop *bool) {
			if e := catch("command", recover()); e != nil {
				croak(e.message)
				*stop = false
			}
		}(&stop)
		stop = k.OneCmd_core(ctx, line)
		return
	}
}

// helpOutput handles Go multiline literals that may have a leading \n
// to make them more readable in source. It just clips off any leading \n.
func (rs *Reposurgeon) helpOutput(help string) {
	if help[0] == '\n' {
		help = help[1:]
	}
	control.baton.printLogString(help)
}

func (rs *Reposurgeon) inScript() bool {
	return len(rs.callstack) > 0
}

//
// Command implementation begins here
//

// DoEOF is the handler for end of command input.
func (rs *Reposurgeon) DoEOF(lineIn string) bool {
	if rs.inputIsStdin {
		respond(control.lineSep)
	}
	return true
}

// HelpQuit says "Shut up, golint!"
func (rs *Reposurgeon) HelpQuit() {
	rs.helpOutput(`
quit

Terminate reposurgeon cleanly.
`)
}

// DoQuit is the handler for the "quit" command.
func (rs *Reposurgeon) DoQuit(lineIn string) bool {
	return true
}

//
// Housekeeping hooks.
//
var inlineCommentRE = regexp.MustCompile(`\s+#`)

func (rs *Reposurgeon) buildPrompt() {
	rs.cmd.SetPrompt("reposurgeon% ")
}

// PreLoop is the hook run before the first command prompt is issued
func (rs *Reposurgeon) PreLoop() {
	rs.buildPrompt()
}

// PreCmd is the hook issued before each command handler
func (rs *Reposurgeon) PreCmd(line string) string {
	trimmed := strings.TrimRight(line, " \t\n")
	if len(trimmed) != 0 {
		rs.history = append(rs.history, trimmed)
	}
	if control.flagOptions["echo"] {
		control.baton.printLogString(trimmed + control.lineSep)
	}
	if strings.HasPrefix(line, "#") {
		return ""
	}
	line = inlineCommentRE.Split(line, 2)[0]

	defer func(line *string) {
		if e := catch("command", recover()); e != nil {
			croak(e.message)
			*line = ""
		}
	}(&line)

	// nil means that the user specified no
	// specific selection set. each command has a
	// different notion of what to do in that
	// case; some bail out, others operate on the
	// whole repository, etc.
	rs.selection = nil
	machine, rest := rs.parseSelectionSet(line)
	if rs.chosen() != nil {
		if machine != nil {
			rs.selection = rs.evalSelectionSet(machine, rs.chosen())
		}
	}

	rs.logHighwater = control.logcounter
	rs.buildPrompt()

	if len(rs.callstack) == 0 {
		control.setAbort(false)
	}
	return rest
}

// PostCmd is the hook executed after each command handler
func (rs *Reposurgeon) PostCmd(stop bool, lineIn string) bool {
	if control.logcounter > rs.logHighwater {
		respond("%d new log message(s)", control.logcounter-rs.logHighwater)
	}
	control.baton.Sync()
	return stop
}

// HelpShell says "Shut up, golint!"
func (rs *Reposurgeon) HelpShell() {
	rs.helpOutput(`
shell [COMMAND-TEXT]

Run a shell command. Honors the $SHELL environment variable.
`)
}

// DoShell is the handler for the "shell" command.
func (rs *Reposurgeon) DoShell(line string) bool {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if logEnable(logCOMMANDS) {
		logit("Spawning %s -c %#v...", shell, line)
	}
	cmd := exec.Command(shell, "-c", line)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		croak("spawn of %s returned error: %v", shell, err)
	}
	return false
}

func (rs *Reposurgeon) accumulateCommits(subarg *fastOrderedIntSet,
	operation func(*Commit) []CommitLike, recurse bool) *fastOrderedIntSet {
	return rs.chosen().accumulateCommits(subarg, operation, recurse)
}

//
// Helpers
//

// Generate a repository report on all objects with a specified display method.
func (rs *Reposurgeon) reportSelect(parse *LineParse, display func(*LineParse, int, Event) string) {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = repo.all()
	}
	for _, eventid := range selection {
		summary := display(parse, eventid, repo.events[eventid])
		if summary != "" {
			if strings.HasSuffix(summary, control.lineSep) {
				fmt.Fprint(parse.stdout, summary)
			} else {
				fmt.Fprintln(parse.stdout, summary)
			}
		}
		if control.getAbort() {
			break
		}
	}
}

// Grab a whitespace-delimited token from the front of the line.
// Interpret double quotes to protect spaces
func popToken(line string) (string, string) {
	tok := ""
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	inQuotes := false
	escape := ""
	for pos, r := range line {
		if !inQuotes && escape == "" && unicode.IsSpace(r) {
			line = strings.TrimLeftFunc(line[pos:], unicode.IsSpace)
			return tok, line
		}
		s := escape + string(r)
		escape = ""
		if s == "\"" {
			inQuotes = !inQuotes
		} else if s == "\\" {
			escape = "\\"
		} else {
			tok += s
		}
	}
	return tok, ""
}

func (commit *Commit) findSuccessors(path string) []string {
	var here []string
	for _, child := range commit.children() {
		childCommit, ok := child.(*Commit)
		if !ok {
			continue
		}
		for _, fileop := range childCommit.operations() {
			if fileop.op == opM && fileop.Path == path {
				here = append(here, childCommit.mark)
			}
		}
		here = append(here, childCommit.findSuccessors(path)...)
	}
	return here
}

// edit mailboxizes and edits the non-blobs in the selection
// Assumes that rs.chosen() and selection are not None
func (rs *Reposurgeon) edit(selection orderedIntSet, line string) {
	parse := rs.newLineParse(line, orderedStringSet{"stdin", "stdout"})
	defer parse.Closem()
	editor := os.Getenv("EDITOR")
	if parse.line != "" {
		editor = parse.line
	}
	if editor == "" {
		croak("you have not specified an editor and $EDITOR is unset")
		// Fallback on /usr/bin/editor on Debian and
		// derivatives.  See
		// https://www.debian.org/doc/debian-policy/#editors-and-pagers
		editor = "/usr/bin/editor"
		realEditor, err := filepath.EvalSymlinks(editor)
		if err != nil {
			croak(err.Error())
			return
		}
		if islink(editor) && exists(realEditor) {
			respond("using %s -> %s instead", editor, realEditor)

		} else {
			return
		}
		control.setAbort(false)
	}
	// Special case: user selected a single blob
	if len(selection) == 1 && !parse.options.Contains("--blobs") {
		singleton := rs.chosen().events[selection[0]]
		if blob, ok := singleton.(*Blob); ok {
			for _, commit := range rs.chosen().commits(nil) {
				for _, fileop := range commit.operations() {
					if fileop.op == opM && fileop.ref == singleton.getMark() {
						if len(commit.findSuccessors(fileop.Path)) > 0 && !parse.options.Contains("--not-last") {
							croak("beware: not the last 'M %s' on its branch", fileop.Path)
						}
						break
					}
				}
			}
			runProcess(editor+" "+blob.materialize(), "editing")
			// recalculate blob.size
			blob.setBlobfile(blob.getBlobfile(false))
			return
		}
		// Fall through
	}

	file, err1 := ioutil.TempFile(".", "rse")
	if err1 != nil {
		croak("creating tempfile for edit: %v", err1)
		return
	}
	defer os.Remove(file.Name())
	for _, i := range selection {
		event := rs.chosen().events[i]
		switch event.(type) {
		case *Commit:
			file.WriteString(event.(*Commit).emailOut(nil, i, nil))
		case *Tag:
			file.WriteString(event.(*Tag).emailOut(nil, i, nil))
		case *Blob:
			if parse.options.Contains("--blobs") {
				file.WriteString(event.(*Blob).emailOut(nil, i, nil))
			}
		}
	}
	file.Close()
	cmd := exec.Command(editor, file.Name())
	// Can't use LineParse defaults here, one point at the baton.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		croak("running editor: %v", err)
		return
	}
	rs.DoMsgin("<" + file.Name())
}

//
// Command implementation begins here
//

//
// On-line help and instrumentation
//

// HelpResolve says "Shut up, golint!"
func (rs *Reposurgeon) HelpResolve() {
	rs.helpOutput(`
{SELECTION} resolve

Does nothing but resolve a selection-set expression
and report the resulting event-number set to standard
output. The remainder of the line after the command is used
as a label for the output.

Implemented mainly for regression testing, but may be useful
for exploring the selection-set language.
`)
}

// DoResolve displays the set of event numbers generated by a selection set.
func (rs *Reposurgeon) DoResolve(line string) bool {
	if rs.selection == nil {
		respond("No selection\n")
	} else {
		oneOrigin := newOrderedIntSet()
		for _, i := range rs.selection {
			oneOrigin.Add(i + 1)
		}
		if line != "" {
			control.baton.printLogString(fmt.Sprintf("%s: %v\n", line, oneOrigin))
		} else {
			control.baton.printLogString(fmt.Sprintf("%v\n", oneOrigin))
		}
	}
	return false
}

// HelpAssign says "Shut up, golint!"
func (rs *Reposurgeon) HelpAssign() {
	rs.helpOutput(`
{SELECTION} assign [--singleton] [NAME]

Compute a leading selection set and assign it to a symbolic name,
which must follow the assign keyword. It is an error to assign to a
name that is already assigned, or to any existing branch name.
Assignments may be cleared by some sequence mutations (though not by
ordinary deletion); you will see a warning when this occurs.

With no selection set and no argument, list all assignments.
This version accepts output redirection.

If the option --singleton is given, the assignment will throw an error
if the selection set is not a singleton.

Use this to optimize out location and selection computations
that would otherwise be performed repeatedly, e.g. in macro calls.
`)
}

// DoAssign is the handler for the "assign" command,
func (rs *Reposurgeon) DoAssign(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	if rs.selection == nil {
		if line != "" {
			croak("No selection")
			return false
		}
		for n, v := range repo.assignments {
			parse.respond(fmt.Sprintf("%s = %v", n, v))
		}
		return false
	}
	name := strings.TrimSpace(parse.line)
	for key := range repo.assignments {
		if key == name {
			croak("%s has already been set", name)
			return false
		}
	}
	if repo.named(name) != nil {
		croak("%s conflicts with a branch, tag, legacy-ID, date, or previous assignment", name)
		return false
	} else if parse.options.Contains("--singleton") && len(rs.selection) != 1 {
		croak("a singleton selection was required here")
		return false
	} else {
		if repo.assignments == nil {
			repo.assignments = make(map[string]orderedIntSet)
		}
		repo.assignments[name] = rs.selection

	}
	return false
}

// HelpUnassign says "Shut up, golint!"
func (rs *Reposurgeon) HelpUnassign() {
	rs.helpOutput(`
unassign {NAME}

Unassign a symbolic name.  Throws an error if the name is not assigned.
Tab-completes on the list of defined names.
`)
}

// CompleteUnassign is a completion hook across assigned names
func (rs *Reposurgeon) CompleteUnassign(text string) []string {
	repo := rs.chosen()
	out := make([]string, 0)
	if repo != nil {
		for key := range repo.assignments {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// DoUnassign is the handler for the "unassign" command.
func (rs *Reposurgeon) DoUnassign(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	if rs.selection != nil {
		croak("cannot take a selection")
		return false
	}
	name := strings.TrimSpace(line)
	if _, ok := repo.assignments[name]; ok {
		delete(repo.assignments, name)
	} else {
		croak("%s has not been set", name)
		return false
	}
	return false
}

// HelpNames says "Shut up, golint!"
func (rs *Reposurgeon) HelpNames() {
	rs.helpOutput(`
names

List all known symbolic names of branches and tags. Supports > redirection.
`)
}

// DoNames is the handler for the "names" command,
func (rs *Reposurgeon) DoNames(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	branches := rs.chosen().branchset()
	//sortbranches.Sort()
	for _, branch := range branches {
		fmt.Fprintf(parse.stdout, "branch %s\n", branch)
	}
	for _, event := range rs.chosen().events {
		if tag, ok := event.(*Tag); ok {
			fmt.Fprintf(parse.stdout, "tag    %s\n", tag.name)

		}
	}
	return false
}

// HelpHistory says "Shut up, golint!"
func (rs *Reposurgeon) HelpHistory() {
	rs.helpOutput(`
history

Dump your command list from this session so far.
`)
}

// DoHistory is the handler for the "history" command,
func (rs *Reposurgeon) DoHistory(_line string) bool {
	for _, line := range rs.history {
		control.baton.printLogString(line)
	}
	return false
}

// HelpIndex says "Shut up, golint!"
func (rs *Reposurgeon) HelpIndex() {
	rs.helpOutput(`
[SELECTION] index [>OUTFILE]

Display four columns of info on selected objects: their number, their
type, the associate mark (or '-' if no mark) and a summary field
varying by type.  For a branch or tag it's the reference; for a commit
it's the commit branch; for a blob it's the repository path of the
file in the blob.  Supports > redirection.
`)
}

// DoIndex generates a summary listing of objects.
func (rs *Reposurgeon) DoIndex(lineIn string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	// We could do all this logic using reportSelect() and index() methods
	// in the objects, but that would have two disadvantages.  First, we'd
	// get a default-set computation we don't want.  Second, for this
	// function it's helpful to have the method strings close together so
	// we can maintain columnation.
	selection := rs.selection
	if rs.selection == nil {
		selection = repo.all()
	}
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	for _, eventid := range selection {
		event := repo.events[eventid]
		switch e := event.(type) {
		case *Blob:
			fmt.Fprintf(parse.stdout, "%6d blob   %6s    %s\n", eventid+1, e.mark, strings.Join(e.paths(nil), " "))
		case *Commit:
			mark := e.mark
			if mark == "" {
				mark = "-"
			}
			fmt.Fprintf(parse.stdout, "%6d commit %6s    %s\n", eventid+1, mark, e.Branch)
		case *Tag:
			fmt.Fprintf(parse.stdout, "%6d tag    %6s    %4s\n", eventid+1, e.committish, e.name)
		case *Reset:
			committish := e.committish
			if committish == "" {
				committish = "-"
			}
			fmt.Fprintf(parse.stdout, "%6d branch %6s    %s\n", eventid+1, committish, e.ref)
		default:
			fmt.Fprintf(parse.stdout, "     ?             -    %s", e)
		}
	}
	return false
}

func storeProfileName(subject string, name string) {
	if control.profileNames == nil {
		control.profileNames = make(map[string]string)
	}
	if subject == "all" {
		profiles := pprof.Profiles()
		for _, profile := range profiles {
			control.profileNames[profile.Name()] = name
		}
	} else if subject != "cpu" && subject != "trace" {
		control.profileNames[subject] = name
	}
}

func saveAllProfiles() {
	stopCPUProfiling()
	stopTracing()
	for subject, name := range control.profileNames {
		saveProfile(subject, name)
	}
}

func saveProfile(subject string, name string) {
	profile := pprof.Lookup(subject)
	if profile != nil {
		filename := fmt.Sprintf("%s.%s.prof", name, subject)
		f, err := os.Create(filename)
		if err != nil {
			croak("failed to create file %#v [%s]", filename, err)
		} else {
			profile.WriteTo(f, 0)
			respond("%s profile saved to %#v", subject, filename)
		}
	} else {
		respond("tried to save %s profile, but it doesn't seem to exist", subject)
	}
}

func startCPUProfiling(name string) {
	filename := name + ".cpu.prof"
	f, err := os.Create(filename)
	if err != nil {
		croak("failed to create file %#v [%s]", filename, err)
	} else {
		pprof.StartCPUProfile(f)
		respond("cpu profiling enabled and saving to %#v.", filename)
	}
}

func stopCPUProfiling() {
	pprof.StopCPUProfile()
}

func startTracing(name string) {
	filename := name + ".trace.prof"
	f, err := os.Create(filename)
	if err != nil {
		croak("failed to create file %#v [%s]", filename, err)
	} else {
		trace.Start(f)
		respond("tracing enabled and saving to %#v.", filename)
	}
}

func stopTracing() {
	trace.Stop()
}

// HelpProfile says "Shut up, golint!"
func (rs *Reposurgeon) HelpProfile() {
	rs.helpOutput(`
profile [live|start|save] [SUBJECT]

Manages data collection for profiling.

Corresponding subcommands are these:

    profile live [PORT]

	Starts an http server on the specified port which serves
	the profiling data. If no port is specified, it defaults
	to port 1234. Use in combination with pprof:

	    go tool pprof -http=":8080" http://localhost:1234/debug/pprof/<subject>

    profile start {SUBJECT} FILENAME

	Starts the named profiler, and tells it to save to the named
	file, which will be overwritten. Currently only the cpu and
	trace profilers require you to explicitly start them; all the
	others start automatically. For the others, the filename is
	stored and used to automatically save the profile before
	reposurgeon exits.

    profile save {SUBJECT} [FILENAME]

	Saves the data from the named profiler to the named file, which
	will be overwritten. If no filename is specified, this will fall
	back to the filename previously stored by 'profile start'.

For a list of available profile subjects, call this commnd without arguments.
The list is in part extracted from the Go runtime and is subject to change.

For documentation, see https://github.com/google/pprof/blob/master/doc/README.md
`)
}

// DoProfile is the handler for the "profile" command.
func (rs *Reposurgeon) DoProfile(line string) bool {
	profiles := pprof.Profiles()
	names := newStringSet()
	for _, profile := range profiles {
		names.Add(profile.Name())
	}
	names.Add("cpu")
	names.Add("trace")
	names.Add("all")
	if line == "" {
		respond("The available profiles are %v", names)
	} else {
		verb, line := popToken(line)
		switch verb {
		case "live":
			port, _ := popToken(line)
			if port == "" {
				port = "1234"
			}
			go func() {
				http.ListenAndServe("localhost:"+port, nil)
			}()
			respond("pprof server started on http://localhost:%s/debug/pprof", port)
		case "start":
			subject, line := popToken(line)
			storeProfileName(subject, line)
			if !names.Contains(subject) {
				croak("I don't recognize %#v as a profile name. The names I do recognize are %v.", subject, names)
			} else if subject == "all" {
				startCPUProfiling(line)
				startTracing(line)
			} else if subject == "cpu" {
				startCPUProfiling(line)
			} else if subject == "trace" {
				startTracing(line)
			} else {
				respond("The %s profile starts automatically when you start reposurgeon; storing %#v to use as a filename to save the profile before reposurgeon exits.", subject, line)
			}
		case "save":
			subject, line := popToken(line)
			filename, line := popToken(line)
			if filename == "" {
				filename = control.profileNames[subject]
			}
			if !names.Contains(subject) {
				croak("I don't recognize %#v as a profile name. The names I do recognize are %v.", subject, names)
			} else if subject == "all" {
				runtime.GC()
				stopTracing()
				stopCPUProfiling()
				for subject := range names.Iterate() {
					if subject != "all" && subject != "cpu" {
						saveProfile(subject, filename)
					}
				}
				respond("all profiling stopped.")
			} else if subject == "cpu" {
				stopCPUProfiling()
				respond("cpu profiling stopped.")
			} else if subject == "trace" {
				stopTracing()
				respond("tracing stopped.")
			} else {
				saveProfile(subject, filename)
				respond("%s profiling stopped.", subject)
			}
		default:
			croak("I don't know how to %s. Possible verbs are [live, start, save].", verb)
		}
	}
	return false
}

// HelpTiming says "Shut up, golint!"
func (rs *Reposurgeon) HelpTiming() {
	rs.helpOutput(`
timings [MARK-NAME] [>OUTFILE]

Report phase-timing results from repository analysis.

If the command has following text, this creates a new, named time mark
that will be visible in a later report; this may be useful during
long-running conversion recipes.

Supports output redirection/
`)
}

// DoTiming reports repo-analysis times
func (rs *Reposurgeon) DoTiming(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	if parse.line != "" {
		rs.chosen().timings = append(rs.chosen().timings, TimeMark{line, time.Now()})
	}
	rs.repo.dumptimes(parse.stdout)
	return false
}

// HelpMemory says "Shut up, golint!"
func (rs *Reposurgeon) HelpMemory() {
	rs.helpOutput(`
memory [>OUTFILE]

Report memory usage.  Runs a garbage-collect before reporting so the figure will better reflect
storage currently held in loaded repositories; this will not affect the reported high-water
mark.
`)
}

// DoMemory is the handler for the "memory" command.
func (rs *Reposurgeon) DoMemory(line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	var memStats runtime.MemStats
	debug.FreeOSMemory()
	runtime.ReadMemStats(&memStats)
	const MB = 1e6
	fmt.Fprintf(parse.stdout, "Heap: %.2fMB  High water: %.2fMB\n",
		float64(memStats.HeapAlloc)/MB, float64(memStats.TotalAlloc)/MB)
	return false
}

// HelpBench says "Shut up, golint!"
func (rs *Reposurgeon) HelpBench() {
	rs.helpOutput(`
elapsed

Report elapsed time and memory usage in the format expected by repobench. Note: this
comment is not intended for interactive use or to be used by scripts other than repobench.  The
output format may change as repobench does.

Runs a garbage-collect before reporting so the figure will better reflect
storage currently held in loaded repositories; this will not affect the reported high-water
mark.
`)
}

// DoBench is the command ghandler for the "bench" command.
func (rs *Reposurgeon) DoBench(line string) bool {
	var memStats runtime.MemStats
	debug.FreeOSMemory()
	runtime.ReadMemStats(&memStats)
	const MB = 1e6
	fmt.Printf("%d %.2f %.2f %.2f\n",
		control.readLimit, time.Since(control.startTime).Seconds(), float64(memStats.HeapAlloc)/MB, float64(memStats.TotalAlloc)/MB)
	return false
}

//
// Information-gathering
//

// HelpStats says "Shut up, golint!"
func (rs *Reposurgeon) HelpStats() {
	rs.helpOutput(`
sizes [>OUTFILE]

Report size statistics and import/export method information of the
currently chosen repository. Supports > redirection.
`)
}

// DoStats reports information on repositories.
func (rs *Reposurgeon) DoStats(line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	if parse.line == "" {
		if rs.chosen() == nil {
			croak("no repo has been chosen.")
			return false
		}
		parse.line = rs.chosen().name
	}
	for _, name := range parse.Tokens() {
		repo := rs.repoByName(name)
		if repo == nil {
			croak("no such repo as %s", name)
			return false
		}
		var blobs, commits, tags, resets, passthroughs int
		for _, event := range repo.events {
			switch event.(type) {
			case *Blob:
				blobs++
			case *Tag:
				tags++
			case *Reset:
				resets++
			case *Passthrough:
				passthroughs++
			case *Commit:
				commits++
			}
		}
		fmt.Fprintf(parse.stdout, "%s: %.0fK, %d events, %d blobs, %d commits, %d tags, %d resets, %s.\n",
			repo.name, float64(repo.size())/1000.0, len(repo.events),
			blobs, commits, tags, resets,
			rfc3339(repo.readtime))
		if repo.sourcedir != "" {
			fmt.Fprintf(parse.stdout, "  Loaded from %s\n", repo.sourcedir)
		}
		//if repo.vcs {
		//    parse.stdout.WriteString(polystr(repo.vcs) + control.lineSep)
	}
	return false
}

// HelpCount says "Shut up, golint!"
func (rs *Reposurgeon) HelpCount() {
	rs.helpOutput(`
{SELECTION} count [>OUTFILE]

Report a count of items in the selection set. Default set is everything
in the currently-selected repo. Supports > redirection.
`)
}

// DoCount us the command handler for the "count" command.
func (rs *Reposurgeon) DoCount(lineIn string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	fmt.Fprintf(parse.stdout, "%d\n", len(selection))
	return false
}

// HelpList says "Shut up, golint!"
func (rs *Reposurgeon) HelpList() {
	rs.helpOutput(`
[SELECTION] list [>OUTFILE]

Display commits in a human-friendly format; the first column is raw
event numbers, the second a timestamp in local time. If the repository
has legacy IDs, they will be displayed in the third column. The
leading portion of the comment follows. Supports > redirection.
`)
}

// DoList generates a human-friendly listing of objects.
func (rs *Reposurgeon) DoList(lineIn string) bool {
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	w := screenwidth()
	modifiers := orderedStringSet{}
	f := func(p *LineParse, i int, e Event) string {
		c, ok := e.(*Commit)
		if ok {
			return c.lister(modifiers, i, w)
		}
		return ""
	}
	rs.reportSelect(parse, f)
	return false
}

// HelpTip says "Shut up, golint!"
func (rs *Reposurgeon) HelpTip() {
	rs.helpOutput(`
[SELECTION] tip [>OUTFILE]

Display the branch tip names associated with commits in the selection
set.  These will not necessarily be the same as their branch fields
(which will often be tag names if the repo contains either annotated
or lightweight tags).

If a commit is at a branch tip, its tip is its branch name.  If it has
only one child, its tip is the child's tip.  If it has multiple children,
then if there is a child with a matching branch name its tip is the
child's tip.  Otherwise this function throws a recoverable error.

Supports > redirection.
`)
}

// DoTip generates a human-friendly listing of objects.
func (rs *Reposurgeon) DoTip(lineIn string) bool {
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	w := screenwidth()
	modifiers := orderedStringSet{}
	f := func(p *LineParse, i int, e Event) string {
		c, ok := e.(*Commit)
		if ok {
			return c.tip(modifiers, i, w)
		}
		return ""
	}
	rs.reportSelect(parse, f)
	return false
}

// HelpTags says "Shut up, golint!"
func (rs *Reposurgeon) HelpTags() {
	rs.helpOutput(`
[SELECTION] tags {>OUTFILE]

Display tags and resets: three fields, an event number and a type and a name.
Branch tip commits associated with tags are also displayed with the type
field 'commit'. Supports > redirection.
`)
}

// DoTags is the handler for the "tags" command.
func (rs *Reposurgeon) DoTags(lineIn string) bool {
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	w := screenwidth()
	modifiers := orderedStringSet{}
	f := func(p *LineParse, i int, e Event) string {
		// this is pretty stupid; pretend you didn't see it
		switch v := e.(type) {
		case *Commit:
			return v.tags(modifiers, i, w)
		case *Reset:
			return v.tags(modifiers, i, w)
		case *Tag:
			return v.tags(modifiers, i, w)
		default:
			return ""
		}
	}
	rs.reportSelect(parse, f)
	return false
}

// HelpStamp says "Shut up, golint!"
func (rs *Reposurgeon) HelpStamp() {
	rs.helpOutput(`
[SELECTION] stamp [>OUTFILE]

Display full action stamps corresponding to commits in a select.
The stamp is followed by the first line of the commit message.
Supports > redirection.
`)
}

// DoStamp lists action stamps for each element of the selection set
func (rs *Reposurgeon) DoStamp(lineIn string) bool {
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	w := screenwidth()
	modifiers := orderedStringSet{}
	f := func(p *LineParse, i int, e Event) string {
		// this is pretty stupid; pretend you didn't see it
		switch v := e.(type) {
		case *Commit:
			return v.stamp(modifiers, i, w)
		case *Tag:
			return v.stamp(modifiers, i, w)
		default:
			return ""
		}
	}
	rs.reportSelect(parse, f)
	return false
}

// HelpSizes says "Shut up, golint!"
func (rs *Reposurgeon) HelpSizes() {
	rs.helpOutput(`
[SELECTION] sizes [>OUTFILE]

Print a report on data volume per branch; takes a selection set,
defaulting to all events. The numbers tally the size of uncompressed
blobs, commit and tag comments, and other metadata strings (a blob is
counted each time a commit points at it).  Not an exact measure of
storage size: intended mainly as a way to get information on how to
efficiently partition a repository that has become large enough to be
unwieldy. Supports > redirection.
`)
}

// DoSizes reports branch relative sizes.
func (rs *Reposurgeon) DoSizes(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	sizes := make(map[string]int)
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	for _, i := range selection {
		if commit, ok := repo.events[i].(*Commit); ok {
			if _, ok := sizes[commit.Branch]; !ok {
				sizes[commit.Branch] = 0
			}
			sizes[commit.Branch] += len(commit.committer.String())
			for _, author := range commit.authors {
				sizes[commit.Branch] += len(author.String())
			}
			sizes[commit.Branch] += len(commit.Comment)
			for _, fileop := range commit.operations() {
				if fileop.op == opM {
					if !strings.HasPrefix(fileop.ref, ":") {
						// Skip submodule refs
						continue
					}
					ref := repo.markToEvent(fileop.ref)
					if ref == nil {
						croak("internal error: %s should be a blob reference", fileop.ref)
						continue
					}
					sizes[commit.Branch] += int(ref.(*Blob).size)
				}
			}
		} else if tag, ok := repo.events[i].(*Tag); ok {
			commit := repo.markToEvent(tag.committish).(*Commit)
			if commit == nil {
				croak("internal error: target of tag %s is nil", tag.name)
				continue
			}
			if _, ok := sizes[commit.Branch]; !ok {
				sizes[commit.Branch] = 0
			}
			sizes[commit.Branch] += len(tag.tagger.String())
			sizes[commit.Branch] += len(tag.Comment)
		}
	}
	total := 0
	for _, v := range sizes {
		total += v
	}
	sz := func(n int, s string) {
		fmt.Fprintf(parse.stdout, "%12d\t%2.2f%%\t%s\n",
			n, float64(n*100.0)/float64(total), s)
	}
	for key, val := range sizes {
		sz(val, key)
	}
	sz(total, "")
	return false
}

// HelpLint says "Shut up, golint!"
func (rs *Reposurgeon) HelpLint() {
	rs.helpOutput(`
[SELECTION] lint [--OPTION...] [>OUTFILE]

Look for DAG and metadata configurations that may indicate a
problem. Presently can check for: (1) Mid-branch deletes, (2)
disconnected commits, (3) parentless commits, (4) the existence of
multiple roots, (5) committer and author IDs that don't look
well-formed as DVCS IDs, (6) multiple child links with identical
branch labels descending from the same commit, (7) time and
action-stamp collisions.

Give it the -? option for a list of available options.

Supports > redirection.
`)
}

// DoLint looks for possible data malformations in a repo.
func (rs *Reposurgeon) DoLint(line string) (StopOut bool) {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	if parse.options.Contains("--options") || parse.options.Contains("-?") {
		fmt.Fprint(parse.stdout, `
--deletealls    -d     report mid-branch deletealls
--connected     -c     report disconnected commits
--roots         -r     report on multiple roots
--attributions  -a     report on anomalies in usernames and attributions
--uniqueness    -u     report on collisions among action stamps
--options       -?     list available options
`[1:])
		return false
	}
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	var lintmutex sync.Mutex
	unmapped := regexp.MustCompile("^[^@]*$|^[^@]*@" + rs.chosen().uuid + "$")
	shortset := newOrderedStringSet()
	deletealls := newOrderedStringSet()
	disconnected := newOrderedStringSet()
	roots := newOrderedStringSet()
	emptyaddr := newOrderedStringSet()
	emptyname := newOrderedStringSet()
	badaddress := newOrderedStringSet()
	rs.chosen().walkEvents(selection, func(idx int, event Event) {
		commit, iscommit := event.(*Commit)
		if !iscommit {
			return
		}
		if len(commit.operations()) > 0 && commit.operations()[0].op == deleteall && commit.hasChildren() {
			lintmutex.Lock()
			deletealls.Add(fmt.Sprintf("on %s at %s", commit.Branch, commit.idMe()))
			lintmutex.Unlock()
		}
		if !commit.hasParents() && !commit.hasChildren() {
			lintmutex.Lock()
			disconnected.Add(commit.idMe())
			lintmutex.Unlock()
		} else if !commit.hasParents() {
			lintmutex.Lock()
			roots.Add(commit.idMe())
			lintmutex.Unlock()
		}
		if unmapped.MatchString(commit.committer.email) {
			lintmutex.Lock()
			shortset.Add(commit.committer.email)
			lintmutex.Unlock()
		}
		for _, person := range commit.authors {
			lintmutex.Lock()
			if unmapped.MatchString(person.email) {
				shortset.Add(person.email)
			}
			lintmutex.Unlock()
		}
		if commit.committer.email == "" {
			lintmutex.Lock()
			emptyaddr.Add(commit.idMe())
			lintmutex.Unlock()
		} else if !strings.Contains(commit.committer.email, "@") {
			lintmutex.Lock()
			badaddress.Add(commit.idMe())
			lintmutex.Unlock()
		}
		for _, author := range commit.authors {
			if author.email == "" {
				lintmutex.Lock()
				emptyaddr.Add(commit.idMe())
				lintmutex.Unlock()
			} else if !strings.Contains(author.email, "@") {
				lintmutex.Lock()
				badaddress.Add(commit.idMe())
				lintmutex.Unlock()
			}
		}
		if commit.committer.fullname == "" {
			lintmutex.Lock()
			emptyname.Add(commit.idMe())
		}
		for _, author := range commit.authors {
			if author.fullname == "" {
				lintmutex.Lock()
				emptyname.Add(commit.idMe())

			}
		}
	})
	// This check isn't done by default because these are common in Subverrsion repos
	// and do not necessarily indicate a problem.
	if parse.options.Contains("--deletealls") || parse.options.Contains("-d") {
		sort.Strings(deletealls)
		for _, item := range deletealls {
			fmt.Fprintf(parse.stdout, "mid-branch delete: %s\n", item)
		}
	}
	if parse.options.Empty() || parse.options.Contains("--connected") || parse.options.Contains("-c") {
		sort.Strings(disconnected)
		for _, item := range disconnected {
			fmt.Fprintf(parse.stdout, "disconnected commit: %s\n", item)
		}
	}
	if parse.options.Empty() || parse.options.Contains("--roots") || parse.options.Contains("-r") {
		if len(roots) > 1 {
			sort.Strings(roots)
			fmt.Fprintf(parse.stdout, "multiple root commits: %v\n", roots)
		}
	}
	if parse.options.Empty() || parse.options.Contains("--names") || parse.options.Contains("-n") {
		sort.Strings(shortset)
		for _, item := range shortset {
			fmt.Fprintf(parse.stdout, "unknown shortname: %s\n", item)
		}
		sort.Strings(emptyaddr)
		for _, item := range emptyaddr {
			fmt.Fprintf(parse.stdout, "empty committer address: %s\n", item)
		}
		sort.Strings(emptyname)
		for _, item := range emptyname {
			fmt.Fprintf(parse.stdout, "empty committer name: %s\n", item)
		}
		sort.Strings(badaddress)
		for _, item := range badaddress {
			fmt.Fprintf(parse.stdout, "email address missing @: %s\n", item)
		}
	}
	if parse.options.Empty() || parse.options.Contains("--uniqueness") || parse.options.Contains("-u") {
		rs.chosen().checkUniqueness(true, func(s string) {
			fmt.Fprint(parse.stdout, "reposurgeon: "+s+control.lineSep)
		})
	}
	return false
}

//
// Housekeeping
//

// HelpPrefer says "Shut up, golint!"
func (rs *Reposurgeon) HelpPrefer() {
	rs.helpOutput(`
prefer [VCS-NAME]

Report or set (with argument) the preferred type of repository. With
no arguments, describe capabilities of all supported systems. With an
argument (which must be the name of a supported version-control
system, and tab-completes in that list) this has two effects:

First, if there are multiple repositories in a directory you do a read
on, reposurgeon will read the preferred one (otherwise it will
complain that it can't choose among them).

Secondly, this will change reposurgeon's preferred type for output.
This means that you do a write to a directory, it will build a repo of
the preferred type rather than its original type (if it had one).

If no preferred type has been explicitly selected, reading in a
repository (but not a fast-import stream) will implicitly set reposurgeon's
preference to the type of that repository.
`)
}

// CompletePrefer is a completion hook across VCS names
func (rs *Reposurgeon) CompletePrefer(text string) []string {
	out := make([]string, 0)
	for _, x := range vcstypes {
		if x.importer != "" && strings.HasPrefix(x.name, text) {
			out = append(out, x.name)
		}
	}
	sort.Strings(out)
	return out
}

// DoPrefer reports or select the preferred repository type.
func (rs *Reposurgeon) DoPrefer(line string) bool {
	if line == "" {
		for _, vcs := range vcstypes {
			control.baton.printLogString(vcs.String() + control.lineSep)
		}
		for option := range fileFilters {
			control.baton.printLogString(fmt.Sprintf("read and write have a --format=%s option that supports %s files.\n", option, strings.ToTitle(option)))
		}
		extractable := make([]string, 0)
		for _, importer := range importers {
			if importer.visible && importer.basevcs != nil {
				extractable = append(extractable, importer.name)
			}
		}
		if len(extractable) > 0 {
			control.baton.printLogString(fmt.Sprintf("Other systems supported for read only: %s\n\n", strings.Join(extractable, " ")))
		}
	} else {
		known := ""
		rs.preferred = nil
		for _, repotype := range importers {
			if repotype.basevcs != nil && strings.ToLower(line) == repotype.name {
				rs.preferred = repotype.basevcs
				rs.extractor = repotype.engine
				break
			}
			if repotype.visible {
				known += " " + repotype.name
			}
		}
		if rs.preferred == nil {
			croak("known types are: %s\n", known)
		}
	}
	if control.isInteractive() {
		if rs.preferred == nil {
			control.baton.printLogString("No preferred type has been set.\n")
		} else {
			control.baton.printLogString(fmt.Sprintf("%s is the preferred type.\n", rs.preferred.name))
		}
	}
	return false
}

// HelpSourcetype says "Shut up, golint!"
func (rs *Reposurgeon) HelpSourcetype() {
	rs.helpOutput(`
sourcetype [VCS-NAME]

Report (with no arguments) or select (with one argument) the current
repository's source type.  This type is normally set at
repository-read time, but may remain unset if the source was a stream
file. The argument tab-completes in the list of supported systems.

The source type affects the interpretation of legacy IDs (for
purposes of the =N visibility set and the 'references' command) by
controlling the regular expressions used to recognize them. If no
preferred output type has been set, it may also change the output
format of stream files made from the repository.

The repository source type is reliably set when reading a Subversion
stream.
`)
}

// CompleteSourcetype is a completion hook across VCS source types
func (rs *Reposurgeon) CompleteSourcetype(text string) []string {
	out := make([]string, 0)
	for _, x := range importers {
		if x.visible && strings.HasPrefix(x.basevcs.name, text) {
			out = append(out, x.basevcs.name)
		}
	}
	sort.Strings(out)
	return out
}

// DoSourcetype reports or selects the current repository's source type.
func (rs *Reposurgeon) DoSourcetype(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	if line == "" {
		if rs.chosen().vcs != nil {
			fmt.Fprintf(control.baton, "%s: %s\n", repo.name, repo.vcs.name)
		} else {
			fmt.Fprintf(control.baton, "%s: no preferred type.\n", repo.name)
		}
	} else {
		known := ""
		for _, importer := range importers {
			if strings.ToLower(line) == importer.name {
				rs.chosen().vcs = importer.basevcs
				return false
			}
			if importer.visible {
				known += " " + importer.name
			}
		}
		croak("known types are %v.", known)
	}
	return false
}

// HelpGc says "Shut up, golint!"
func (rs *Reposurgeon) HelpGc() {
	rs.helpOutput(`
gc [GOGC]

Trigger a garbage collection. Scavenges and removes all blob objects
that no longer have references, e.g. as a result of delete operqtions
on repositories. This is followed by a Go-runtime garbage collection.

The optional argument, if present, is passed as a
https://golang.org/pkg/runtime/debug/#SetGCPercent[SetPercentGC]
call to the Go runtime. The initial value is 100; setting it lower
causes more frequwent garbage collection and may reduces maximum
working set, while setting it higher causes less frequent garbage
collection and will raise maximum working set.
`)
}

// DoGc is the handler for the "gc" command.
func (rs *Reposurgeon) DoGc(line string) bool {
	for _, repo := range rs.repolist {
		repo.gcBlobs()
	}
	runtime.GC()
	if line != "" {
		v, err := strconv.Atoi(line)
		if err != nil {
			croak("ill-formed numeric argument")
			return false
		}
		debug.SetGCPercent(v)
	}
	return false
}

// HelpChoose says "Shut up, golint!"
func (rs *Reposurgeon) HelpChoose() {
	rs.helpOutput(`
choose [REPO-NAME]

Choose a named repo on which to operate.  The name of a repo is
normally the basename of the directory or file it was loaded from, but
repos loaded from standard input are 'unnamed'. The program will add
a disambiguating suffix if there have been multiple reads from the
same source.

With no argument, lists the names of the currently stored repositories
and their load times.  The second column is '*' for the currently selected
repository, '-' for others.

With an argument, the command tab-completes on the above list.
`)
}

// CompleteChoose as a completion hook across the set of repository names
func (rs *Reposurgeon) CompleteChoose(text string) []string {
	if rs.repolist == nil {
		return nil
	}
	out := make([]string, 0)
	for _, x := range rs.repolist {
		if strings.HasPrefix(x.name, text) {
			out = append(out, x.name)
		}
	}
	sort.Strings(out)
	return out
}

// DoChoose selects a named repo on which to operate.
func (rs *Reposurgeon) DoChoose(line string) bool {
	if rs.selection != nil {
		croak("choose does not take a selection set")
		return false
	}
	if len(rs.repolist) == 0 && len(line) > 0 {
		if control.isInteractive() {
			croak("no repositories are loaded, can't find %q.", line)
			return false
		}
	}
	if line == "" {
		for _, repo := range rs.repolist {
			status := "-"
			if rs.chosen() != nil && repo == rs.chosen() {
				status = "*"
			}
			fmt.Fprintf(control.baton, "%s %s\n", status, repo.name)
		}
	} else {
		if newOrderedStringSet(rs.reponames()...).Contains(line) {
			rs.choose(rs.repoByName(line))
			if control.isInteractive() {
				rs.DoStats(line)
			}
		} else {
			croak("no such repo as %s", line)
		}
	}
	return false
}

// HelpDrop says "Shut up, golint!"
func (rs *Reposurgeon) HelpDrop() {
	rs.helpOutput(`
drop [REPO-NAME]

Drop a repo named by the argument from reposurgeon's list, freeing the memory
used for its metadata and deleting on-disk blobs. With no argument, drops the
currently chosen repo. Tab-completes on the list of loaded repositories.
`)
}

// CompleteDrop is a completion hook across the set of repository names
func (rs *Reposurgeon) CompleteDrop(text string) []string {
	return rs.CompleteChoose(text)
}

// DoDrop drops a repo from reposurgeon's list.
func (rs *Reposurgeon) DoDrop(line string) bool {
	if len(rs.reponames()) == 0 {
		if control.isInteractive() {
			croak("no repositories are loaded.")
			return false
		}
	}
	if rs.selection != nil {
		croak("drop does not take a selection set")
		return false
	}
	if line == "" {
		if rs.chosen() == nil {
			croak("no repo has been chosen.")
			return false
		}
		line = rs.chosen().name
	}
	if rs.reponames().Contains(line) {
		if rs.chosen() != nil && line == rs.chosen().name {
			rs.unchoose()
		}
		holdrepo := rs.repoByName(line)
		holdrepo.cleanup()
		rs.removeByName(line)
	} else {
		croak("no such repo as %s", line)
	}
	if control.isInteractive() && !control.flagOptions["quiet"] {
		// Emit listing of remaining repos
		rs.DoChoose("")
	}
	return false
}

// HelpRename says "Shut up, golint!"
func (rs *Reposurgeon) HelpRename() {
	rs.helpOutput(`
rename {NEW-NAME}

Rename the currently chosen repo; requires an argument.  Won't do it
if there is already one by the new name.
`)
}

// DoRename changes the name of a repository.
func (rs *Reposurgeon) DoRename(line string) bool {
	if rs.selection != nil {
		croak("rename does not take a selection set")
		return false
	}
	if rs.reponames().Contains(line) {
		croak("there is already a repo named %s.", line)
	} else if rs.chosen() == nil {
		croak("no repository is currently chosen.")
	} else {
		rs.chosen().rename(line)

	}
	return false
}

// HelpPreserve says "Shut up, golint!"
func (rs *Reposurgeon) HelpPreserve() {
	rs.helpOutput(`
preserve [PATH...]

Add (presumably untracked) files or directories to the repo's list of
paths to be restored from the backup directory after a rebuild. Each
argument, if any, is interpreted as a pathname.  The current preserve
list is displayed afterwards.
`)
}

// DoPreserve adds files and subdirectories to the preserve set.
func (rs *Reposurgeon) DoPreserve(line string) bool {
	if rs.selection != nil {
		croak("preserve does not take a selection set")
		return false
	}
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	for _, filename := range strings.Fields(line) {
		rs.chosen().preserve(filename)
	}
	respond("preserving %s.", rs.chosen().preservable())
	return false
}

// HelpUnpreserve says "Shut up, golint!"
func (rs *Reposurgeon) HelpUnpreserve() {
	rs.helpOutput(`
unpreserve [PATH...]

Remove (presumably untracked) files or directories to the repo's list
of paths to be restored from the backup directory after a
rebuild. Each argument, if any, is interpreted as a pathname.  The
current preserve list is displayed afterwards.
`)
}

// DoUnpreserve removes files and subdirectories from the preserve set.
func (rs *Reposurgeon) DoUnpreserve(line string) bool {
	if rs.selection != nil {
		croak("unpreserve does not take a selection set")
		return false
	}
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	for _, filename := range strings.Fields(line) {
		rs.chosen().unpreserve(filename)
	}
	respond("preserving %s.", rs.chosen().preservable())
	return false
}

//
// Serialization and de-serialization.
//

// HelpRead says "Shut up, golint!"
func (rs *Reposurgeon) HelpRead() {
	rs.helpOutput(`
read  [--OPTION...] [<INFILE | DIRECTORY]

A read command with no arguments is treated as 'read .', operating on the
current directory.

With a directory-name argument, this command attempts to read in the
contents of a repository in any supported version-control system under
that directory.

If input is redirected from a plain file, it will be read in as a
fast-import stream or Subversion dump, whichever it is.

With an argument of '-', this command reads a fast-import stream or
Subversion dump from standard input (this will be useful in filters
constructed with command-line arguments).

The --format option can be used to read in binary repository dump files.
For a list of supported types, invoke the 'prefer' command.
`)
}

// DoRead reads in a repository for surgery.
func (rs *Reposurgeon) DoRead(line string) bool {
	if rs.selection != nil {
		croak("read does not take a selection set")
		return false
	}
	parse := rs.newLineParse(line, []string{"stdin"})
	// Don't do parse.Closem() here - you'll nuke the seaakstream that
	// we use to get content out of dump streams.
	var repo *Repository
	if parse.redirected {
		repo = newRepository("")
		for _, option := range parse.options {
			if strings.HasPrefix(option, "--format=") {
				_, vcs := splitRuneFirst(option, '=')
				infilter, ok := fileFilters[vcs]
				if !ok {
					croak("unrecognized --format")
					return false
				}
				srcname := "unknown-in"
				if f, ok := parse.stdin.(*os.File); ok {
					srcname = f.Name()
				}
				// parse is redirected so this
				// must be something besides
				// os.Stdin, so we can close
				// it and substitute another
				// redirect
				parse.stdin.Close()
				command := fmt.Sprintf(infilter.importer, srcname)
				reader, _, err := readFromProcess(command)
				if err != nil {
					croak("can't open filter: %v", infilter)
					return false
				}
				parse.stdin = reader
				break
			}
		}
		repo.fastImport(context.TODO(), parse.stdin, parse.options.toStringSet(), "")
	} else if parse.line == "" || parse.line == "." {
		var err2 error
		// This is slightly asymmetrical with the write side, which
		// interprets an empty argument list as '-'
		cdir, err2 := os.Getwd()
		if err2 != nil {
			croak(err2.Error())
			return false
		}
		repo, err2 = readRepo(cdir, parse.options.toStringSet(), rs.preferred, rs.extractor, control.flagOptions["quiet"])
		if err2 != nil {
			croak(err2.Error())
			return false
		}
	} else if isdir(parse.line) {
		var err2 error
		repo, err2 = readRepo(parse.line, parse.options.toStringSet(), rs.preferred, rs.extractor, control.flagOptions["quiet"])
		if err2 != nil {
			croak(err2.Error())
			return false
		}
	} else {
		croak("read no longer takes a filename argument - use < redirection instead")
		return false
	}
	rs.repolist = append(rs.repolist, repo)
	rs.choose(repo)
	if rs.chosen() != nil {
		if rs.chosen().vcs != nil {
			rs.preferred = rs.chosen().vcs
		}
		name := rs.chosen().sourcedir
		if name == "" {
			name = parse.infile
			if name == "" {
				name = "unnamed"
			}
		}
		rs.chosen().rename(rs.uniquify(filepath.Base(name)))
	}
	if control.isInteractive() && !control.flagOptions["quiet"] {
		rs.DoChoose("")
	}
	return false
}

// HelpWrite says "Shut up, golint!"
func (rs *Reposurgeon) HelpWrite() {
	rs.helpOutput(`
[SELECTION] write [--legacy] [--format=fossil] [--noincremental] [--callout]  [>OUTFILE|-]

Dump a fast-import stream representing selected events to standard
output (if second argument is empty or '-') or via > redirect to a file.
Alternatively, if there ia no redirect and the argument names a
directory the repository is rebuilt into that directory, with any
selection set argument being ignored; if that target directory is
nonempty its contents are backed up to a save directory.

Property extensions will be omitted if the importer for the
preferred repository type cannot digest them.

The --fossil option can be used to write out binary repository dump files.
For a list of supported types, invoke the 'prefer' command.
`)
}

// DoWrite streams out the results of repo surgery.
func (rs *Reposurgeon) DoWrite(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	// Python also handled prefix ~user
	if strings.HasPrefix(line, "~/") {
		usr, err := user.Current()
		if err == nil {
			line = usr.HomeDir + line[1:]
		}
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	// This is slightly asymmetrical with the read side, which
	// interprets an empty argument list as '.'
	if parse.redirected || parse.line == "" {
		for _, option := range parse.options {
			if strings.HasPrefix(option, "--format=") {
				_, vcs := splitRuneFirst(option, '=')
				outfilter, ok := fileFilters[vcs]
				if !ok {
					croak("unrecognized --format")
					return false
				}
				srcname := "unknown-out"
				if f, ok := parse.stdout.(*os.File); ok {
					srcname = f.Name()
				}
				// parse is redirected so this
				// must be something besides
				// os.Stdout, so we can close
				// it and substitute another
				// redirect
				parse.stdout.Close()
				command := fmt.Sprintf(outfilter.exporter, srcname)
				writer, _, err := writeToProcess(command)
				if err != nil {
					croak("can't open output filter: %v", outfilter)
					return false
				}
				parse.stdout = writer
				break
			}
		}
		rs.chosen().fastExport(rs.selection, parse.stdout, parse.options.toStringSet(), rs.preferred)
	} else if isdir(parse.line) {
		err := rs.chosen().rebuildRepo(parse.line, parse.options.toStringSet(), rs.preferred)
		if err != nil {
			croak(err.Error())
		}
	} else {
		croak("write no longer takes a filename argument - use > redirection instead")
	}
	return false
}

// HelpInspect says "Shut up, golint!"
func (rs *Reposurgeon) HelpInspect() {
	rs.helpOutput(`
[SELECTION] inspect

Dump a fast-import stream representing selected events to standard
output or via > redirect to a file.  Just like a write, except (1) the
progress meter is disabled, and (2) there is an identifying header
before each event dump.
`)
}

// DoInspect dumps raw events.
func (rs *Reposurgeon) DoInspect(lineIn string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}

	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()

	selection := rs.selection
	if selection == nil {
		state := rs.evalState(len(repo.events))
		defer state.release()
		selection, parse.line = rs.parse(parse.line, state)
		if selection == nil {
			selection = repo.all()
		}
	}
	for _, eventid := range selection {
		event := repo.events[eventid]
		header := fmt.Sprintf("Event %d %s\n", eventid+1, strings.Repeat("=", 72))
		fmt.Fprintln(parse.stdout, header[:73])
		fmt.Fprint(parse.stdout, event.String())
	}

	return false
}

// HelpStrip says "Shut up, golint!"
func (rs *Reposurgeon) HelpStrip() {
	rs.helpOutput(`
[SELECTION] strip {--blobs|--reduce}

Replace the blobs in the selected repository with self-identifying stubs;
and/or strip out topologically uninteresting commits.  The options for
this are '--blobs' and '--reduce' respectively; the default is '--blobs'.

A selection set is effective only with the '--blobs' option, defaulting to all
blobs. The '--reduce' mode always acts on the entire repository.

This is intended for producing reduced test cases from large repositories.
`)
}

// CompleteStrip is a completion hook across strip's modifiers.
func (rs *Reposurgeon) CompleteStrip(text string) []string {
	return []string{"--blobs", "--reduce"}
}

// DoStrip strips out content to produce a reduced test case.
func (rs *Reposurgeon) DoStrip(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	var striptypes orderedStringSet
	var oldlen int
	if line == "" {
		striptypes = orderedStringSet{"--blobs"}
	} else {
		striptypes = newOrderedStringSet(strings.Fields(line)...)
	}
	if striptypes.Contains("--blobs") {
		for _, ei := range selection {
			if blob, ok := repo.events[ei].(*Blob); ok {
				blob.setContent([]byte(fmt.Sprintf("Blob at %s\n", blob.mark)), noOffset)
			}
		}
	}
	if striptypes.Contains("--reduce") {
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
		oldlen = len(repo.events)
		deletia := newOrderedIntSet()
		for i, event := range repo.events {
			if commit, ok := event.(*Commit); ok && !interesting.Contains(commit.mark) {
				deletia.Add(i)
			}
		}
		repo.delete(deletia, nil)
		respond("From %d to %d events.", oldlen, len(repo.events))
	}
	return false
}

// HelpGraph says "Shut up, golint!"
func (rs *Reposurgeon) HelpGraph() {
	rs.helpOutput(`
[SELECTION] graph

Dump a graph representing selected events to standard output in DOT markup
for graphviz. Supports > redirection.
`)
}

// Most comment characters we want to fit in a commit box
const graphCaptionLength = 32

// DoGraph dumps a commit graph.
func (rs *Reposurgeon) DoGraph(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	fmt.Fprint(parse.stdout, "digraph {\n")
	for _, ei := range selection {
		event := rs.chosen().events[ei]
		if commit, ok := event.(*Commit); ok {
			for _, parent := range commit.parentMarks() {
				if selection.Contains(rs.chosen().markToIndex(parent)) {
					fmt.Fprintf(parse.stdout, "\t%s -> %s;\n",
						parent[1:], commit.mark[1:])
				}
			}
		}
		if tag, ok := event.(*Tag); ok {
			fmt.Fprintf(parse.stdout, "\t\"%s\" -> \"%s\" [style=dotted];\n",
				tag.name, tag.committish[1:])
			fmt.Fprintf(parse.stdout, "\t{rank=same; \"%s\"; \"%s\"}\n",
				tag.name, tag.committish[1:])
		}
	}
	for _, ei := range selection {
		event := rs.chosen().events[ei]
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
			fmt.Fprintf(parse.stdout, "\t%s [shape=box,width=5,label=<<table cellspacing=\"0\" border=\"0\" cellborder=\"0\"><tr><td><font color=\"blue\">%s</font></td><td>%s</td></tr></table>>];\n",
				commit.mark[1:], cid, summary)
			newbranch := true
			for _, cchild := range commit.children() {
				if child, ok := cchild.(*Commit); ok && commit.Branch == child.Branch {
					newbranch = false
				}
			}
			if newbranch {
				fmt.Fprintf(parse.stdout, "\t\"%s\" [shape=oval,width=2];\n", commit.Branch)
				fmt.Fprintf(parse.stdout, "\t\"%s\" -> \"%s\" [style=dotted];\n", commit.mark[1:], commit.Branch)
			}
		}
		if tag, ok := event.(*Tag); ok {
			firstLine, _ := splitRuneFirst(tag.Comment, '\n')
			if len(firstLine) >= graphCaptionLength {
				firstLine = firstLine[:graphCaptionLength]
			}
			summary := html.EscapeString(firstLine)
			fmt.Fprintf(parse.stdout, "\t\"%s\" [label=<<table cellspacing=\"0\" border=\"0\" cellborder=\"0\"><tr><td><font color=\"blue\">%s</font></td><td>%s</td></tr></table>>];\n", tag.name, tag.name, summary)
		}
	}
	fmt.Fprint(parse.stdout, "}\n")
	return false
}

// HelpRebuild says "Shut up, golint!"
func (rs *Reposurgeon) HelpRebuild() {
	rs.helpOutput(`
rebuild {DIRECTORY}

Rebuild a repository from the state held by reposurgeon.  The argument
specifies the target directory in which to do the rebuild; if the
repository read was from a repo directory (and not a git-import stream), it
defaults to that directory.  If the target directory is nonempty
its contents are backed up to a save directory.
`)
}

// DoRebuild rebuilds a live repository from the edited state.
func (rs *Reposurgeon) DoRebuild(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if len(rs.selection) != 0 {
		croak("rebuild does not take a selection set")
		return false
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	err := rs.chosen().rebuildRepo(parse.line, parse.options.toStringSet(), rs.preferred)
	if err != nil {
		croak(err.Error())
	}
	return false
}

//
// Editing commands
//

// HelpMsgout says "Shut up, golint!"
func (rs *Reposurgeon) HelpMsgout() {
	rs.helpOutput(`
[SELECTION] msgout [--filter=/regexp/] [--blobs]

Emit a file of messages in RFC822 format representing the contents of
repository metadata. Takes a selection set; members of the set other
than commits, annotated tags, and passthroughs are ignored (that is,
presently, blobs and resets). Supports > redirection.

May have an option --filter, followed by = and a /-enclosed regular expression.
If this is given, only headers with names matching it are emitted.  In this
control the name of the header includes its trailing colon.

Blobs may be included in the output with the option --blobs.
`)
}

// DoMsgout generates a message-box file representing object metadata.
func (rs *Reposurgeon) DoMsgout(lineIn string) bool {
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()

	var filterRegexp *regexp.Regexp
	s, present := parse.OptVal("--filter")
	if present {
		if len(s) >= 2 && strings.HasPrefix(s, "/") && strings.HasSuffix(s, "/") {
			var err error
			payload := s[1 : len(s)-1]
			filterRegexp, err = regexp.Compile(payload)
			if err != nil {
				croak("malformed filter option %q in msgout\n", payload)
				return false
			}
		} else {
			croak("malformed filter option %q in msgout\n", s)
			return false
		}
	}
	f := func(p *LineParse, i int, e Event) string {
		// this is pretty stupid; pretend you didn't see it
		switch v := e.(type) {
		case *Passthrough:
			return v.emailOut(orderedStringSet{}, i, filterRegexp)
		case *Commit:
			return v.emailOut(orderedStringSet{}, i, filterRegexp)
		case *Tag:
			return v.emailOut(orderedStringSet{}, i, filterRegexp)
		case *Blob:
			if parse.options.Contains("--blobs") {
				return v.emailOut(orderedStringSet{}, i, filterRegexp)
			}
			return ""
		default:
			return ""
		}
	}
	rs.reportSelect(parse, f)
	return false
}

// HelpMsgin says "Shut up, golint!"
func (rs *Reposurgeon) HelpMsgin() {
	rs.helpOutput(`
msgin [--create] [<INFILE]

Accept a file of messages in RFC822 format representing the
contents of the metadata in selected commits and annotated tags. Takes
no selection set. If there is an argument it will be taken as the name
of a message-box file to read from; if no argument, or one of '-', reads
from standard input. Supports < redirection.

Users should be aware that modifying an Event-Number or Event-Mark field
will change which event the update from that message is applied to.  This
is unlikely to have good results.

The header CheckText, if present, is examined to see if the comment
text of the associated event begins with it. If not, the item
modification is aborted. This helps ensure that you are landing
updates on the events you intend.

If the --create modifier is present, new tags and commits will be
appended to the repository.  In this case it is an error for a tag
name to match any exting tag name. Commit objects are created with no
fileops.  If Committer-Date or Tagger-Date fields are not present they
are filled in with the time at which this command is executed. If
Committer or Tagger fields are not present, reposurgeon will attempt
to deduce the user's git-style identity and fill it in. If a singleton
commit set was specified for commit creations, the new commits are
made children of that commit.

Otherwise, if the Event-Number and Event-Mark fields are absent, the
msgin logic will attempt to match the commit or tag first by Legacy-ID,
then by a unique committer ID and timestamp pair.

If output is redirected and the modifier '--changed' appears, a minimal
set of modifications actually made is written to the output file in a form
that can be fed back in. Supports > redirection.

If the option --empty-only is given, this command will throw a recoverable error
if it tries to alter a message body that is neither empty nor consists of the
CVS empty-comment marker.
`)
}

// DoMsgin accepts a message-box file representing object metadata and update from it.
func (rs *Reposurgeon) DoMsgin(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	parse := rs.newLineParse(line, orderedStringSet{"stdin", "stdout"})
	defer parse.Closem()
	repo.readMessageBox(rs.selection, parse.stdin, parse.stdout,
		parse.options.Contains("--create"),
		parse.options.Contains("--empty-only"),
		parse.options.Contains("--changed"))
	return false
}

// HelpEdit says "Shut up, golint!"
func (rs *Reposurgeon) HelpEdit() {
	rs.helpOutput(`
[SELECTION] edit [<INFILE] [>OUTFILE]

Report the selection set of events to a tempfile as msgout does,
call an editor on it, and update from the result as msgin does.
If you do not specify an editor name as second argument, it will be
taken from the $EDITOR variable in your environment.
If $EDITOR is not set, /usr/bin/editor will be used as a fallback
if it exists as a symlink to your default editor, as is the case on
Debian, Ubuntu and their derivatives.

Normally this command ignores blobs because msgout does.
However, if you specify a selection set consisting of a single
blob, your editor will be called on the blob file; alternatively,
as with msgout, the --blobs option will include blobs in the file.

Supports < and > redirection.
`)
}

// DoEdit edits metadata interactively.
func (rs *Reposurgeon) DoEdit(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	rs.edit(selection, line)
	return false
}

// HelpFilter says "Shut up, golint!"
//FIXME: Move dedos to transcode?
func (rs *Reposurgeon) HelpFilter() {
	rs.helpOutput(`
[SELECTION] filter [--dedos|--shell|--regexp|--replace] [TEXT-OR-REGEXP]

Run blobs, commit comments and committer/author names, or tag comments
and tag committer names in the selection set through the filter
specified on the command line.

With any mode other than --dedos, attempting to specify a selection
set including both blobs and non-blobs (that is, commits or tags)
throws an error. Inline content in commits is filtered when the
selection set contains (only) blobs and the commit is within the range
bounded by the earliest and latest blob in the specification.

When filtering blobs, if the command line contains the magic cookie
'%PATHS%' it is replaced with a space-separated list of all paths
that reference the blob.

With --shell, the remainder of the line specifies a filter as a
shell command. Each blob or comment is presented to the filter on
standard input; the content is replaced with whatever the filter emits
to standard output.

With --regex, the remainder of the line is expected to be a Go
regular expression substitution written as /from/to/ with 'from' and
'to' being passed as arguments to the standard re.sub() function and
that applied to modify the content. Actually, any non-space character
will work as a delimiter in place of the /; this makes it easier to
use / in patterns. Ordinarily only the first such substitution is
performed; putting 'g' after the slash replaces globally, and a
numeric literal gives the maximum number of substitutions to
perform. Other flags available restrict substitution scope - 'c' for
comment text only, 'C' for committer name only, 'a' for author names
only.

With --replace, the behavior is like --regexp but the expressions are
not interpreted as regular expressions. (This is slightly faster).

With --dedos, DOS/Windows-style \r\n line terminators are replaced with \n.
`)
}

type filterCommand struct {
	repo       *Repository
	filtercmd  string
	sub        func(string) string
	regexp     *regexp.Regexp
	attributes orderedStringSet
}

// GoReplacer bridges from Python-style back-references (\1) to Go-style ($1).
// This was originally a shim for testing during the port from Python.  It has
// been kept because Go's use of $n for group matches conflicts with the
// use of $n for script arguments in reposurgeon.
func GoReplacer(re *regexp.Regexp, fromString, toString string) string {
	for i := 0; i < 10; i++ {
		sdigit := fmt.Sprintf("%d", i)
		toString = strings.Replace(toString, `\`+sdigit, `${`+sdigit+`}`, -1)
	}
	out := re.ReplaceAllString(fromString, toString)
	return out
}

// newFilterCommand - Initialize a filter from the command line.
func newFilterCommand(repo *Repository, filtercmd string) *filterCommand {
	fc := new(filterCommand)
	fc.repo = repo
	fc.attributes = newOrderedStringSet()
	// Must not use LineParse here as it would try to strip options
	// in shell commands.
	flagRe := regexp.MustCompile(`[0-9]*g?`)
	if strings.HasPrefix(filtercmd, "--shell") {
		fc.filtercmd = strings.TrimSpace(filtercmd[7:])
		fc.attributes = newOrderedStringSet("c", "a", "C")
	} else if strings.HasPrefix(filtercmd, "--regex") || strings.HasPrefix(filtercmd, "--replace") {
		firstspace := strings.Index(filtercmd, " ")
		if firstspace == -1 {
			croak("missing filter specification")
			return nil
		}
		stripped := strings.TrimSpace(filtercmd[firstspace:])
		parts := strings.Split(stripped, stripped[0:1])
		subflags := parts[len(parts)-1]
		if len(parts) != 4 {
			croak("malformed filter specification")
			return nil
		} else if parts[0] != "" {
			croak("bad prefix %q on filter specification", parts[0])
			return nil
		} else if subflags != "" && !flagRe.MatchString(subflags) {
			croak("unrecognized filter flags")
			return nil
		} else {
			subcount := 1
			for _, flag := range subflags {
				if flag == 'g' {
					subcount = -1
				} else if flag == 'c' || flag == 'a' || flag == 'C' {
					fc.attributes.Add(string(flag))
				} else if i := strings.IndexRune("0123456789", flag); i != -1 {
					subcount = i
				} else {
					croak("unknown filter flag")
					return nil
				}
			}
			if len(fc.attributes) == 0 {
				fc.attributes = newOrderedStringSet("c", "a", "C")
			}
			if strings.HasPrefix(filtercmd, "--regex") {
				pattern := parts[1]
				var err error
				fc.regexp, err = regexp.Compile(pattern)
				if err != nil {
					croak("filter compilation error: %v", err)
					return nil
				}
				fc.sub = func(s string) string {
					if subcount == -1 {
						return GoReplacer(fc.regexp, s, parts[2])
					}
					replacecount := subcount
					replacer := func(s string) string {
						replacecount--
						if replacecount > -1 {
							return GoReplacer(fc.regexp, s, parts[2])
						}
						return s
					}
					return fc.regexp.ReplaceAllStringFunc(s, replacer)
				}
			} else if strings.HasPrefix(filtercmd, "--replace") {
				fc.sub = func(s string) string {
					return strings.Replace(s, parts[1], parts[2], subcount)
				}
			}
		}
	} else if strings.HasPrefix(filtercmd, "--dedos") {
		if len(fc.attributes) == 0 {
			fc.attributes = newOrderedStringSet("c", "a", "C")
		}
		fc.sub = func(s string) string {
			out := strings.Replace(s, "\r\n", "\n", -1)
			return out
		}
	} else {
		croak("--shell or --regex or --dedos required")
		return nil
	}
	return fc
}

func (fc *filterCommand) do(content string, substitutions map[string]string) string {
	// Perform the filter on string content or a file.
	if fc.filtercmd != "" {
		substituted := fc.filtercmd
		for k, v := range substitutions {
			substituted = strings.Replace(substituted, k, v, -1)
		}
		cmd := exec.Command("sh", "-c", substituted)
		cmd.Stdin = strings.NewReader(content)
		content, err := cmd.Output()
		if err != nil {
			if logEnable(logWARN) {
				logit("filter command failed")
			}
		}
		return string(content)
	} else if fc.sub != nil {
		return fc.sub(content)
	} else {
		if logEnable(logWARN) {
			logit("unknown mode in filter command")
		}
	}
	return content
}

// DoFilter  is rtthe handler for the "filter" command.
func (rs *Reposurgeon) DoFilter(line string) (StopOut bool) {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	if line == "" {
		croak("no filter is specified")
		return false
	}
	if rs.selection == nil {
		croak("no selection")
		return false
	}
	// Mainline of do_filter() continues {
	filterhook := newFilterCommand(rs.chosen(), line)
	if filterhook != nil {
		rs.chosen().dataTraverse("Filtering",
			rs.selection,
			filterhook.do,
			filterhook.attributes,
			!strings.HasPrefix(line, "--dedos"),
			rs.inScript())
	}
	return false
}

// HelpTranscode says "Shut up, golint!"
func (rs *Reposurgeon) HelpTranscode() {
	rs.helpOutput(`
[SELECTION] transcode {ENCODING}

Transcode blobs, commit comments and committer/author names, or tag
comments and tag committer names in the selection set to UTF-8 from
the character encoding specified on the command line.

Attempting to specify a selection set including both blobs and
non-blobs (that is, commits or tags) throws an error. Inline content
in commits is filtered when the selection set contains (only) blobs
and the commit is within the range bounded by the earliest and latest
blob in the specification.

The encoding argument must name one of the codecs known to the Go
standard codecs library. In particular, 'latin-1' is a valid codec name.

Errors in this command force the repository to be dropped, because an
error may leave repository objects in a damaged state.
`)
}

// DoTranscode is the handler for the "transcode" command.
func (rs *Reposurgeon) DoTranscode(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}

	enc, err := ianaindex.IANA.Encoding(line)
	if err != nil {
		croak("can's set up codec %s: error %v", line, err)
		return false
	}
	decoder := enc.NewDecoder()

	transcode := func(txt string, _ map[string]string) string {
		out, err := decoder.Bytes([]byte(txt))
		if err != nil {
			if logEnable(logWARN) {
				logit("decode error during transcoding: %v", err)
			}
			rs.unchoose()
		}
		return string(out)
	}
	rs.chosen().dataTraverse("Transcoding",
		rs.selection,
		transcode,
		newOrderedStringSet("c", "a", "C"),
		true, !rs.inScript())
	return false
}

// HelpSetfield says "Shut up, golint!"
func (rs *Reposurgeon) HelpSetfield() {
	rs.helpOutput(`
[SELECTION] setfield {FIELD} {VALUE}

In the selected objects (defaulting to none) set every instance of a
named field to a string value.  The string may be quoted to include
whitespace, and use backslash escapes interpreted by Go's C-like
string-escape codec, such as \s.

Attempts to set nonexistent attributes are ignored. Valid values for
the attribute are internal field names; in particular, for commits,
'comment' and 'branch' are legal.  Consult the source code for other
interesting values.

The special fieldnames 'author', 'commitdate' and 'authdate' apply
only to commits in the range.  The latter two sets attribution
dates. The former sets the author's name and email address (assuming
the value can be parsed for both), copying the committer
timestamp. The author's timezone may be deduced from the email
address.
`)
}

// DoSetfield sets an object field from a string.
func (rs *Reposurgeon) DoSetfield(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	repo := rs.chosen()
	if rs.selection == nil {
		croak("no selection")
		return false
	}
	fields, err := shlex.Split(line, true)
	if err != nil || len(fields) != 2 {
		croak("missing or malformed setfield line")
	}
	// Caling strings,Title so that Python-sytyle (uncapitalized)
	// fieldnsmes will still work.
	field := strings.Title(fields[0])
	value, err := stringEscape(fields[1])
	if err != nil {
		croak("while setting field: %v", err)
		return false
	}
	for _, ei := range rs.selection {
		event := repo.events[ei]
		if _, ok := getAttr(event, field); ok {
			setAttr(event, field, value)
			if event.isCommit() {
				event.(*Commit).hash.invalidate()
			}
		} else if commit, ok := event.(*Commit); ok {
			if field == "Author" {
				attr := value
				attr += " " + commit.committer.date.String()
				newattr, _ := newAttribution("")
				commit.authors = append(commit.authors, *newattr)
			} else if field == "Commitdate" {
				newdate, err := newDate(value)
				if err != nil {
					croak(err.Error())
					return false
				}
				commit.committer.date = newdate
			} else if field == "Authdate" {
				newdate, err := newDate(value)
				if err != nil {
					croak(err.Error())
					return false
				}
				commit.authors[0].date = newdate
			}
			commit.hash.invalidate()
		}
	}
	return false
}

// HelpSetperm says "Shut up, golint!"
func (rs *Reposurgeon) HelpSetperm() {
	rs.helpOutput(`
[SELECTION] setperm {PERM} [PATH...]

For the selected objects (defaulting to none) take the first argument as an
octal literal describing permissions.  All subsequent arguments are paths.
For each M fileop in the selection set and exactly matching one of the
paths, patch the permission field to the first argument value.
`)
}

// DoSetperm alters permissions on M fileops matching a path list.
func (rs *Reposurgeon) DoSetperm(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	if rs.selection == nil {
		croak("no selection")
		return false
	}
	fields, err := shlex.Split(line, true)
	if err != nil {
		croak("failurev in line pesing: %v", err)
		return false
	}
	if len(fields) < 2 {
		croak("missing or malformed setperm line")
		return false
	}
	perm := fields[0]
	paths := newOrderedStringSet(fields[1:]...)
	if !newOrderedStringSet("100644", "100755", "120000").Contains(perm) {
		croak("unexpected permission literal %s", perm)
		return false
	}
	baton := control.baton
	//baton.startProcess("patching modes", "")
	for _, ei := range rs.selection {
		if commit, ok := rs.chosen().events[ei].(*Commit); ok {
			for i, op := range commit.fileops {
				if op.op == opM && paths.Contains(op.Path) {
					commit.fileops[i].mode = perm
				}
			}
			baton.twirl()
		}
	}
	//baton.endProcess()
	return false
}

// HelpAppend says "Shut up, golint!"
func (rs *Reposurgeon) HelpAppend() {
	rs.helpOutput(`
[SELECTION] append [--rstrip] {TEXT}

Append text to the comments of commits and tags in the specified
selection set. The text is the first token of the command and may
be a quoted string. C-style escape sequences in the string are
interpreted using Go's Quote/Unquote codec from the strconv library.

If the option --rstrip is given, the comment is right-stripped before
the new text is appended. If the option --legacy is given, the string
%LEGACY% in the append payload is replaced with the commit's lagacy-ID
before it is appended.
`)
}

// DoAppend appends a specified line to comments in the specified selection set.
func (rs *Reposurgeon) DoAppend(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	if rs.selection == nil {
		croak("no selection")
		return false
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	fields, err := shlex.Split(parse.line, true)
	if err != nil {
		croak(err.Error())
		return false
	}
	if len(fields) == 0 {
		croak("missing append line")
		return false
	}
	line, err = stringEscape(fields[0])
	if err != nil {
		croak(err.Error())
		return false
	}
	for _, ei := range rs.selection {
		event := rs.chosen().events[ei]
		switch event.(type) {
		case *Commit:
			commit := event.(*Commit)
			if parse.options.Contains("--rstrip") {
				commit.Comment = strings.TrimRight(commit.Comment, " \n\t")
			}
			if parse.options.Contains("--legacy") {
				commit.Comment += strings.Replace(line, "%LEGACY%", commit.legacyID, -1)
			} else {
				commit.Comment += line
			}
		case *Tag:
			tag := event.(*Tag)
			if parse.options.Contains("--rstrip") {
				tag.Comment = strings.TrimRight(tag.Comment, " \n\t")
			}
			tag.Comment += line
		}
	}
	return false
}

// HelpSquash says "Shut up, golint!"
func (rs *Reposurgeon) HelpSquash() {
	rs.helpOutput(`
[SELECTION] squash [--POLICY...]

Combine a selection set of events; this may mean deleting them or
pushing their content forward or back onto a target commit just
outside the selection range, depending on policy flags.

The default selection set for this command is empty.  Blobs cannot be
directly affected by this command; they move or are deleted only when
removal of fileops associated with commits requires this.
`)
}

// DoSquash squashes events in the specified selection set.
func (rs *Reposurgeon) DoSquash(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	if rs.selection == nil {
		rs.selection = nil
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	rs.chosen().squash(rs.selection, parse.options)
	return false
}

// HelpDelete says "Shut up, golint!"
func (rs *Reposurgeon) HelpDelete() {
	rs.helpOutput(`
[SELECTION] delete

Delete a selection set of events.  The default selection set for this
command is empty.  Tags, resets, and passthroughs are deleted with no
side effects.  Blobs cannot be directly deleted with this command; they
are removed only when removal of fileops associated with commits requires this.

When a commit is deleted, what becomes of tags and fileops attached to
it is controlled by policy flags.  A delete is equivalent to a
squash with the --delete flag.
`)
}

// DoDelete is the handler for the "delete" command.
func (rs *Reposurgeon) DoDelete(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	if rs.selection == nil {
		rs.selection = nil
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	parse.options.Add("--delete")
	rs.chosen().squash(rs.selection, parse.options)
	return false
}

// HelpCoalesce says "Shut up, golint!"
func (rs *Reposurgeon) HelpCoalesce() {
	rs.helpOutput(`
[SELECTION] coalesce [--debug]

Scan the selection set (defaulting to all) for runs of commits with
identical comments close to each other in time (this is a common form
of scar tissues in repository up-conversions from older file-oriented
version-control systems).  Merge these cliques by pushing their
fileops and tags up to the last commit, in order.

The optional argument, if present, is a maximum time separation in
seconds; the default is 90 seconds.

With the --changelog option, any commit with a comment containing the
string 'empty log message' (such as is generated by CVS) and containing
exactly one file operation modifying a path ending in 'ChangeLog' is
treated specially.  Such ChangeLog commits are considered to match any
commit before them by content, and will coalesce with it if the committer
matches and the commit separation is small enough.  This option handles
a convention used by Free Software Foundation projects.

With  the --debug option, show messages about mismatches.
`)
}

// DoCoalesce coalesces events in the specified selection set.
func (rs *Reposurgeon) DoCoalesce(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo is loaded")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	timefuzz := 90
	changelog := parse.options.Contains("--changelog")
	if parse.line != "" {
		var err error
		timefuzz, err = strconv.Atoi(parse.line)
		if err != nil {
			croak("time-fuzz value must be an integer")
			return false
		}
	}
	isChangelog := func(commit *Commit) bool {
		return strings.Contains(commit.Comment, "empty log message") && len(commit.operations()) == 1 && commit.operations()[0].op == opM && strings.HasSuffix(commit.operations()[0].Path, "ChangeLog")
	}
	coalesceMatch := func(cthis *Commit, cnext *Commit) bool {
		croakOnFail := logEnable(logDELETE) || parse.options.Contains("--debug")
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
		repo.squash(squashable, orderedStringSet{})
	}
	respond("%d spans coalesced.", len(squashes))
	return false
}

// HelpAdd says "Shut up, golint!"
func (rs *Reposurgeon) HelpAdd() {
	rs.helpOutput(`
{SELECTION} add M {PERM} {MARK} {PATH}

{SELECTION} add D {PATH}

{SELECTION} add R {SOURCE} {TARGET}

{SELECTION} add C {SOURCE} {TARGET}

From a specified commit, add a specified fileop.

For a D operation to be valid there must be an M operation for the path
in the commit's ancestry.  For an M operation to be valid, the 'perm'
part must be a token ending with 755 or 644 and the 'mark' must
refer to a blob that precedes the commit location.  For an R or C
operation to be valid, there must be an M operation for the source
in the commit's ancestry.

`)
}

// DoAdd adds a fileop to a specified commit.
func (rs *Reposurgeon) DoAdd(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	repo := rs.chosen()
	fields, err := shlex.Split(line, true)
	if err != nil && len(fields) < 2 {
		croak("add requires an operation type and arguments")
		return false
	}
	optype := optype(fields[0][0])
	var perms, argpath, mark, source, target string
	if optype == opD {
		argpath = fields[1]
		for _, event := range repo.commits(rs.selection) {
			if event.paths(nil).Contains(argpath) {
				croak("%s already has an op for %s",
					event.mark, argpath)
				return false
			}
			if event.ancestorCount(argpath) == 0 {
				croak("no previous M for %s", argpath)
				return false
			}
		}
	} else if optype == opM {
		if len(fields) != 4 {
			croak("wrong field count in add command")
			return false
		} else if strings.HasSuffix(fields[1], "644") {
			perms = "100644"
		} else if strings.HasSuffix(fields[1], "755") {
			perms = "100755"
		}
		mark = fields[2]
		if !strings.HasPrefix(mark, ":") {
			croak("garbled mark %s in add command", mark)
			return false
		}
		markval, err1 := strconv.Atoi(mark[1:])
		if err1 != nil {
			croak("non-numeric mark %s in add command", mark)
			return false
		}
		blob, ok := repo.markToEvent(mark).(*Blob)
		if !ok {
			croak("mark %s in add command does not refer to a blob", mark)
			return false
		} else if markval >= rs.selection.Min() {
			croak("mark %s in add command is after add location", mark)
			return false
		}
		argpath = fields[3]
		for _, event := range repo.commits(rs.selection) {
			if event.paths(nil).Contains(argpath) {
				croak("%s already has an op for %s",
					blob.mark, argpath)
				return false
			}
		}
	} else if optype == opR || optype == opC {
		if len(fields) < 3 {
			croak("too few arguments in add %c", optype)
			return false
		}
		source = fields[1]
		target = fields[2]
		for _, event := range repo.commits(rs.selection) {
			if event.paths(nil).Contains(source) || event.paths(nil).Contains(target) {
				croak("%s already has an op for %s or %s",
					event.mark, source, target)
				return false
			}
			if event.ancestorCount(source) == 0 {
				croak("no previous M for %s", source)
				return false
			}
		}
	} else {
		croak("unknown operation type %c in add command", optype)
		return false
	}
	for _, commit := range repo.commits(rs.selection) {
		fileop := newFileOp(rs.chosen())
		if optype == opD {
			fileop.construct(opD, argpath)
		} else if optype == opM {
			fileop.construct(opM, perms, mark, argpath)
		} else if optype == opR || optype == opC {
			fileop.construct(optype, source, target)
		}
		commit.appendOperation(fileop)
	}
	return false
}

// HelpBlob says "Shut up, golint!"
func (rs *Reposurgeon) HelpBlob() {
	rs.helpOutput(`
blob

Create a blob at mark :1 after renumbering other marks starting from
:2.  Data is taken from stdin, which may be a here-doc.  This can be
used with the add command to patch data into a repository.
`)
}

// DoBlob adds a fileop to a specified commit.
func (rs *Reposurgeon) DoBlob(line string) bool {
	if rs.chosen() == nil {
		croak("no repo is loaded")
		return false
	}
	repo := rs.chosen()
	repo.renumber(2, nil)
	blob := newBlob(repo)
	blob.setMark(":1")
	repo.insertEvent(blob, len(repo.frontEvents()), "adding blob")
	parse := rs.newLineParse(line, orderedStringSet{"stdin"})
	defer parse.Closem()
	content, err := ioutil.ReadAll(parse.stdin)
	if err != nil {
		croak("while reading blob content: %v", err)
		return false
	}
	blob.setContent(content, noOffset)
	repo.declareSequenceMutation("adding blob")
	repo.invalidateNamecache()
	return false
}

// HelpRemove says "Shut up, golint!"
// FIXME: Odd syntax
func (rs *Reposurgeon) HelpRemove() {
	rs.helpOutput(`
[SELECTION] remove [DMRCN] {OP} [to {SELECTION}]

From a specified commit, remove a specified fileop. The syntax:

The *op* must be one of (a) the keyword 'deletes', (b) a file path, (c)
a file path preceded by an op type set (some subset of the letters
DMRCN), or (c) a 1-origin numeric index.  The 'deletes' keyword
selects all D fileops in the commit; the others select one each.

If the to clause is present, the removed op is appended to the
commit specified by the following singleton selection set.  This option
cannot be combined with 'deletes'.

Note that this command does not attempt to scavenge blobs even if the
deleted fileop might be the only reference to them. This behavior may
change in a future release.
`)
}

// DoRemove deletes a fileop from a specified commit.
func (rs *Reposurgeon) DoRemove(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo is loaded")
		return false
	}
	if rs.selection == nil {
		rs.selection = newOrderedIntSet()
	}
	orig := line
	opindex, line := popToken(line)
	optypes := "DMRCN"
	regex := regexp.MustCompile("^[DMRCN]+$")
	match := regex.FindStringIndex(opindex)
	if match != nil {
		optypes = opindex[match[0]:match[1]]
		opindex, line = popToken(line)
	}
	for _, ie := range rs.selection {
		ev := repo.events[ie]
		event, ok := ev.(*Commit)
		if !ok {
			croak("Event %d is not a commit.", ie+1)
			return false
		}
		if opindex == "deletes" {
			ops := make([]*FileOp, 0)
			for _, op := range event.operations() {
				if op.op != opD {
					ops = append(ops, op)
				}
			}
			event.setOperations(ops)
			return false
		}
		ind := -1
		// first, see if opindex matches the filenames of any
		// of this event's operations
		for i, op := range event.operations() {
			if !strings.Contains(optypes, string(op.op)) {
				continue
			}
			if op.Path == opindex || op.Source == opindex {
				ind = i
				break
			}
		}
		// otherwise, perhaps it's an integer
		if ind == -1 {
			var err error
			ind, err = strconv.Atoi(opindex)
			ind--
			if err != nil {
				croak("invalid or missing fileop specification '%s' on %s", opindex, orig)
				return false
			}
		}
		target := -1
		if line != "" {
			verb, line := popToken(line)
			if verb == "to" {
				rs.setSelectionSet(line)
				if len(rs.selection) != 1 {
					croak("remove to requires a singleton selection")
					return false
				}
				target = rs.selection[0]
			}
		}
		ops := event.operations()
		present := ind >= 0 && ind < len(ops)
		if !present {
			croak("out-of-range fileop index %d", ind)
			return false
		}
		removed := ops[ind]
		event.fileops = append(ops[:ind], ops[ind+1:]...)
		if target == -1 {
			if removed.op == opM {
				repo.markToEvent(removed.ref).(*Blob).removeOperation(removed)
			}
		} else {
			present := target >= 0 && target < len(repo.events)
			if !present {
				croak("out-of-range target event %d", target+1)
				return false
			}
			commit, ok := repo.events[target].(*Commit)
			if !ok {
				croak("event %d is not a commit", target+1)
				return false
			}
			commit.appendOperation(removed)
			// Blob might have to move, too - we need to keep the
			// relocated op from having an unresolvable forward
			// mark reference.
			if removed.ref != "" && target < ie {
				i := repo.markToIndex(removed.ref)
				blob := repo.events[i]
				repo.events = append(repo.events[:i], repo.events[i+1:]...)
				repo.insertEvent(blob, target, "blob move")
			}
			// FIXME: Scavenge blobs left with no references
		}
	}
	return false
}

// HelpRenumber says "Shut up, golint!"
func (rs *Reposurgeon) HelpRenumber() {
	rs.helpOutput(`
renumber

Renumber the marks in a repository, from :1 up to <n> where <n> is the
count of the last mark. Just in case an importer ever cares about mark
ordering or gaps in the sequence.
`)
}

// DoRenumber is he handler for the "renumber" command.
func (rs *Reposurgeon) DoRenumber(line string) bool {
	// Renumber the marks in the selected repo.
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	rs.repo.renumber(1, nil)
	return false
}

// HelpDedup says "Shut up, golint!"
func (rs *Reposurgeon) HelpDedup() {
	rs.helpOutput(`
[SELECTION] dedup

Deduplicate blobs in the selection set.  If multiple blobs in the selection
set have the same SHA1, throw away all but the first, and change fileops
referencing them to instead reference the (kept) first blob.
`)
}

// DoDedup deduplicates identical (up to hash) blobs within the selection set
func (rs *Reposurgeon) DoDedup(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	blobMap := make(map[string]string) // hash -> mark
	dupMap := make(map[string]string)  // duplicate mark -> canonical mark
	for _, ei := range selection {
		event := rs.chosen().events[ei]
		if blob, ok := event.(*Blob); ok {
			sha := blob.gitHash().hexify()
			if blobMap[sha] != "" {
				dupMap[blob.mark] = blobMap[sha]
			} else {
				blobMap[sha] = blob.mark
			}
		}
		control.baton.twirl()
	}
	rs.chosen().dedup(dupMap)
	return false
}

// HelpTimeoffset says "Shut up, golint!"
func (rs *Reposurgeon) HelpTimeoffset() {
	rs.helpOutput(`
[SELECTION] timeoffset {OFFSET}

Apply a time offset to all time/date stamps in the selected set.  An offset
argument is required; it may be in the form [+-]ss, [+-]mm:ss or [+-]hh:mm:ss.
The leading sign is optional. With no argument, the default is 1 second.

Optionally you may also specify another argument in the form [+-]hhmm, a
timeone literal to apply.  To apply a timezone without an offset, use
an offset literal of 0, +0 or -0.
`)
}

// DoTimeoffset applies a time offset to all dates in selected events.
func (rs *Reposurgeon) DoTimeoffset(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	offsetOf := func(hhmmss string) (int, error) {
		h := "0"
		m := "0"
		s := "0"
		if strings.Count(hhmmss, ":") == 0 {
			s = hhmmss
		} else if strings.Count(hhmmss, ":") == 1 {
			fields := strings.SplitN(hhmmss, ":", 3)
			m = fields[0]
			s = fields[1]
		} else if strings.Count(hhmmss, ":") == 2 {
			fields := strings.SplitN(hhmmss, ":", 4)
			h = fields[0]
			m = fields[1]
			s = fields[2]
		} else {
			croak("too many colons")
			return 0, errors.New("too many colons")
		}
		hn, err := strconv.Atoi(h)
		if err != nil {
			croak("bad literal in hour field")
			return 0, err
		}
		mn, err1 := strconv.Atoi(m)
		if err1 != nil {
			croak("bad literal in minute field")
			return 0, err1
		}
		sn, err2 := strconv.Atoi(s)
		if err2 != nil {
			croak("bad literal in seconds field")
			return 0, err2
		}
		return hn*3600 + mn*60 + sn, nil
	}
	args := strings.Fields(line)
	var loc *time.Location
	var offset time.Duration
	var noffset int
	if len(args) == 0 {
		noffset = 1
		offset = time.Second
	} else {
		var err error
		noffset, err = offsetOf(args[0])
		if err != nil {
			return false
		}
		offset = time.Duration(noffset) * time.Second
	}
	if len(args) > 1 {
		tr := regexp.MustCompile(`[+-][0-9][0-9][0-9][0-9]`)
		if !tr.MatchString(args[1]) {
			croak("expected timezone literal to be [+-]hhmm")
			return false
		}
		zoffset, err1 := offsetOf(args[1])
		if err1 != nil {
			return false
		}
		loc = time.FixedZone(args[1], zoffset)
	}
	for _, ei := range selection {
		event := rs.chosen().events[ei]
		if tag, ok := event.(*Tag); ok {
			if tag.tagger != nil {
				tag.tagger.date.timestamp = tag.tagger.date.timestamp.Add(offset)
				if len(args) > 1 {
					tag.tagger.date.timestamp = tag.tagger.date.timestamp.In(loc)
				}
			}
		} else if commit, ok := event.(*Commit); ok {
			commit.bump(noffset)
			if len(args) > 1 {
				commit.committer.date.timestamp = commit.committer.date.timestamp.In(loc)
			}
			for _, author := range commit.authors {
				if len(args) > 1 {
					author.date.timestamp = author.date.timestamp.In(loc)
				}
			}
		}
	}
	return false
}

// HelpWhen says "Shut up, golint!"
func (rs *Reposurgeon) HelpWhen() {
	rs.helpOutput(`
when {TIMESTAMP}

Interconvert between git timestamps (integer Unix time plus TZ) and
RFC3339 format.  Takes one argument, autodetects the format.  Useful
when eyeballing export streams.  Also accepts any other supported
date format and converts to RFC3339.
`)
}

// DoWhen uis thee command handler for the "when" command.
func (rs *Reposurgeon) DoWhen(LineIn string) (StopOut bool) {
	if LineIn == "" {
		croak("a supported date format is required.")
		return false
	}
	d, err := newDate(LineIn)
	if err != nil {
		croak("unrecognized date format")
	} else if strings.Contains(LineIn, "Z") {
		control.baton.printLogString(d.String())
	} else {
		control.baton.printLogString(d.rfc3339())
	}
	return false
}

// HelpDivide says "Shut up, golint!"
func (rs *Reposurgeon) HelpDivide() {
	rs.helpOutput(`
{SELECTION} divide

Attempt to partition a repo by cutting the parent-child link
between two specified commits (they must be adjacent). Does not take a
general selection-set argument.  It is only necessary to specify the
parent commit, unless it has multiple children in which case the child
commit must follow (separate it with a comma).

If the repo was named 'foo', you will normally end up with two repos
named 'foo-early' and 'foo-late'.  But if the commit graph would
remain connected through another path after the cut, the behavior
changes.  In this case, if the parent and child were on the same
branch 'qux', the branch segments are renamed 'qux-early' and
'qux-late'.
`)
}

// DoDivide is the command handler for the "divide" command.
func (rs *Reposurgeon) DoDivide(_line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if len(rs.selection) == 0 {
		croak("one or possibly two arguments specifying a link are required")
		return false
	}
	earlyEvent := rs.chosen().events[rs.selection[0]]
	earlyCommit, ok := earlyEvent.(*Commit)
	if !ok {
		croak("first element of selection is not a commit")
		return false
	}
	possibles := earlyCommit.children()
	var late Event
	var lateCommit *Commit
	if len(rs.selection) == 1 {
		if len(possibles) > 1 {
			croak("commit has multiple children, one must be specified")
			return false
		} else if len(possibles) == 1 {
			late = possibles[0]
			lateCommit, ok = late.(*Commit)
			if !ok {
				croak("only child of selected commit is not a commit")
				return false
			}
		} else {
			croak("parent has no children")
			return false
		}
	} else if len(rs.selection) == 2 {
		late = rs.chosen().events[rs.selection[1]]
		lateCommit, ok = late.(*Commit)
		if !ok {
			croak("last element of selection is not a commit")
			return false
		}
		if !orderedStringSet(lateCommit.parentMarks()).Contains(earlyCommit.mark) {
			croak("not a parent-child pair")
			return false
		}
	} else if len(rs.selection) > 2 {
		croak("too many arguments")
	}
	//assert(early && late)
	rs.selection = nil
	// Try the topological cut first
	if rs.cut(earlyCommit, lateCommit) {
		respond("topological cut succeeded")
	} else {
		// If that failed, cut anyway and rename the branch segments
		lateCommit.removeParent(earlyCommit)
		if earlyCommit.Branch != lateCommit.Branch {
			respond("no branch renames were required")
		} else {
			basename := earlyCommit.Branch
			respond("%s has been split into %s-early and %s-late",
				basename, basename, basename)
			for i, event := range rs.chosen().events {
				if commit, ok := event.(*Commit); ok && commit.Branch == basename {
					if i <= rs.selection[0] {
						commit.Branch += "-early"
					} else {
						commit.Branch += "-late"
					}
				}
			}
		}
	}
	if control.isInteractive() && !control.flagOptions["quiet"] {
		rs.DoChoose("")
	}
	return false
}

// HelpExpunge says "Shut up, golint!"
func (rs *Reposurgeon) HelpExpunge() {
	rs.helpOutput(`
[SELECTION] expunge [~] [/PATTERN/...]

Expunge files from the selected portion of the repo history; the
default is the entire history.  The arguments to this command may be
paths or regular expressions matching paths (regexps must
be marked by being surrounded with //).  String quotes and backslash
escapes are interpreted when parsing the command line.

Exceptionally, the first argument may be the token "~" which chooses
all file paths other than those selected by the remaining arguments to
ne expunged.  You may use this to sift out all file operations
matching a pattern set rather than expunging them.

All filemodify (M) operations and delete (D) operations involving a
matched file in the selected set of events are disconnected from the
repo and put in a removal set.  Renames are followed as the tool walks
forward in the selection set; each triggers a warning message. If a
selected file is a copy (C) target, the copy will be deleted and a
warning message issued. If a selected file is a copy source, the copy
target will be added to the list of paths to be deleted and a warning
issued.

After file expunges have been performed, any commits with no
remaining file operations will be deleted, and any tags pointing to
them. By default each deleted commit is replaced with a tag of the form
emptycommit-<ident> on the preceding commit unless --notagify is
specified as an argument.  Commits with deleted fileops pointing both
in and outside the path set are not deleted, but are cloned into the
removal set.
`)
}

// DoExpunge expunges files from the chosen repository.
func (rs *Reposurgeon) DoExpunge(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()

	}
	fields, err := shlex.Split(line, true)
	if err != nil {
		croak("malformed expunge command")
		return false
	}
	err = rs.expunge(selection, fields)
	if err != nil {
		respond(err.Error())
	}
	return false
}

// HelpSplit says "Shut up, golint!"
//FIXME: Odd syntax
func (rs *Reposurgeon) HelpSplit() {
	rs.helpOutput(`
[SELECTION] split at {M}

[SELECTION] split by {PREFIX}

Split a specified commit in two, the opposite of squash.

The selection set is required to be a commit location; the modifier is
a preposition which indicates which splitting method to use. If the
preposition is 'at', then the third argument must be an integer
1-origin index of a file operation within the commit. If it is 'by',
then the third argument must be a pathname to be matched.

The commit is copied and inserted into a new position in the
event sequence, immediately following itself; the duplicate becomes
the child of the original, and replaces it as parent of the original's
children. Commit metadata is duplicated; the mark of the new commit is
then changed.  If the new commit has a legacy ID, the suffix '.split' is
appended to it.

Finally, some file operations - starting at the one matched or indexed
by the split argument - are moved forward from the original commit
into the new one.  Legal indices are 2-n, where n is the number of
file operations in the original commit.
`)
}

// DoSplit splits a commit.
func (rs *Reposurgeon) DoSplit(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if len(rs.selection) != 1 {
		croak("selection of a single commit required for this command")
		return false
	}
	where := rs.selection[0]
	event := rs.chosen().events[where]
	commit, ok := event.(*Commit)
	if !ok {
		croak("selection doesn't point at a commit")
		return false
	}
	fields := strings.Fields(line)
	prep := fields[0]
	obj := fields[1]
	if len(fields) != 2 {
		croak("ill-formed split command")
		return false
	}
	if prep == "at" {
		splitpoint, err := strconv.Atoi(obj)
		if err != nil {
			croak("expected integer fileop index (1-origin)")
			return false
		}
		splitpoint--
		if splitpoint > len(commit.operations()) {
			croak("fileop index %d out of range", splitpoint)
			return false
		}
		err = rs.chosen().splitCommitByIndex(where, splitpoint)
		if err != nil {
			croak(err.Error())
			return false
		}
	} else if prep == "by" {
		err := rs.chosen().splitCommitByPrefix(where, obj)
		if err != nil {
			croak(err.Error())
			return false
		}
	} else {
		croak("don't know what to do for preposition %s", prep)
		return false
	}
	respond("new commits are events %d and %d.", where+1, where+2)
	return false
}

// HelpUnite says "Shut up, golint!"
func (rs *Reposurgeon) HelpUnite() {
	rs.helpOutput(`
unite [--prune] [REPO-NAME...]

Unite repositories. Name any number of loaded repositories; they will
be united into one union repo and removed from the load list.  The
union repo will be selected.

The root of each repo (other than the oldest repo) will be grafted as
a child to the last commit in the dump with a preceding commit date.
This will produce a union repository with one branch for each part.
Running last to first, tag and branch duplicate names will be
disambiguated using the source repository name (thus, recent
duplicates will get priority over older ones). After all grafts, marks
will be renumbered.

The name of the new repo will be the names of all parts concatenated,
separated by '+'. It will have no source directory or preferred system
type.

With the option --prune, at each join generate D ops for every
file that doesn't have a modify operation in the root commit of the
branch being grafted on.
`)
}

// DoUnite melds repos together.
func (rs *Reposurgeon) DoUnite(line string) bool {
	rs.unchoose()
	factors := make([]*Repository, 0)
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	for _, name := range strings.Fields(parse.line) {
		repo := rs.repoByName(name)
		if repo == nil {
			croak("no such repo as %s", name)
			return false
		}
		factors = append(factors, repo)
	}
	if len(factors) < 2 {
		croak("unite requires two or more repo name arguments")
		return false
	}
	rs.unite(factors, parse.options.toStringSet())
	if control.isInteractive() && !control.flagOptions["quiet"] {
		rs.DoChoose("")
	}
	return false
}

// HelpGraft says "Shut up, golint!"
func (rs *Reposurgeon) HelpGraft() {
	rs.helpOutput(`
[SELECTION] graft [--prune] REPO-NAME

For when unite doesn't give you enough control. This command may have
either of two forms, selected by the size of the selection set.  The
first argument is always required to be the name of a loaded repo.

If the selection set is of size 1, it must identify a single commit in
the currently chosen repo; in this case the named repo's root will
become a child of the specified commit. If the selection set is
empty, the named repo must contain one or more callouts matching a
commits in the currently chosen repo.

Labels and branches in the named repo are prefixed with its name; then
it is grafted to the selected one. Any other callouts in the named repo are also
resolved in the control of the currently chosen one. Finally, the
named repo is removed from the load list.

With the option --prune, prepend a deleteall operation into the root
of the grafted repository.
`)
}

// DoGraft grafts a named repo onto the selected one.
func (rs *Reposurgeon) DoGraft(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if len(rs.repolist) == 0 {
		croak("no repositories are loaded.")
		return false
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	graftRepo := rs.repoByName(parse.line)
	requireGraftPoint := true
	var graftPoint int
	if rs.selection != nil && len(rs.selection) == 1 {
		graftPoint = rs.selection[0]
	} else {
		for _, commit := range graftRepo.commits(nil) {
			for _, parent := range commit.parents() {
				if isCallout(parent.getMark()) {
					requireGraftPoint = false
				}
			}
		}
		if !requireGraftPoint {
			graftPoint = invalidGraftIndex
		} else {
			croak("a singleton selection set is required.")
			return false
		}
	}
	// OK, we've got the two repos and the graft point.  Do it.
	rs.chosen().graft(graftRepo, graftPoint, parse.options.toStringSet())
	rs.removeByName(graftRepo.name)
	return false
}

// HelpDebranch says "Shut up, golint!"
func (rs *Reposurgeon) HelpDebranch() {
	rs.helpOutput(`
debranch {SOURCE-BRANCH} [TARGET-BRANCH]

Takes one or two arguments which must be the names of source and target
branches; if the second (target) argument is omitted it defaults to 'master'.
The history of the source branch is merged into the history of the target
branch, becoming the history of a subdirectory with the name of the source
branch. Any trailing segment of a branch name is accepted as a synonym for
it; thus 'master' is the same as 'refs/heads/master'.  Any resets of the
source branch are removed.
`)
}

// DoDebranch turns a branch into a subdirectory.
func (rs *Reposurgeon) DoDebranch(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	args := strings.Fields(line)
	if len(args) == 0 {
		croak("debranch command requires at least one argument")
		return false
	}
	target := "refs/heads/master"
	source := args[0]
	if len(args) == 2 {
		target = args[1]
	}
	repo := rs.chosen()
	branches := repo.branchmap()
	if branches[source] == "" {
		for candidate := range branches {
			if strings.HasSuffix(candidate, string(os.PathSeparator)+source) {
				source = candidate
				goto found1
			}
		}
		croak("no branch matches source %s", source)
		return false
	found1:
	}
	if branches[target] == "" {
		for candidate := range branches {
			if strings.HasSuffix(candidate, string(os.PathSeparator)+target) {
				target = candidate
				goto found2
			}
		}
		croak("no branch matches %s", target)
		return false
	found2:
	}
	// Now that the arguments are in proper form, implement
	stip := repo.markToIndex(branches[source])
	scommits := append(repo.ancestors(stip), stip)
	sort.Ints(scommits)
	ttip := repo.markToIndex(branches[target])
	tcommits := append(repo.ancestors(ttip), ttip)
	sort.Ints(tcommits)
	// Don't touch commits up to the branch join.
	lastParent := make([]string, 0)
	for len(scommits) > 0 && len(tcommits) > 0 && scommits[0] == tcommits[0] {
		lastParent = []string{repo.events[scommits[0]].getMark()}
		scommits = scommits[1:]
		tcommits = tcommits[1:]
	}
	pref := filepath.Base(source)
	for _, ci := range scommits {
		for idx := range repo.events[ci].(*Commit).operations() {
			fileop := repo.events[ci].(*Commit).fileops[idx]
			fileop.Path = filepath.Join(pref, fileop.Path)
			if fileop.op == opR || fileop.op == opC {
				fileop.Source = filepath.Join(pref, fileop.Source)
			}
		}
	}
	merged := append(scommits, tcommits...)
	sort.Ints(merged)
	sourceReset := -1
	for _, i := range merged {
		commit := repo.events[i].(*Commit)
		if len(lastParent) > 0 {
			trailingMarks := commit.parentMarks()
			if len(trailingMarks) > 0 {
				trailingMarks = trailingMarks[1:]
			}
			commit.setParentMarks(append(lastParent, trailingMarks...))
		}
		commit.setBranch(target)
		lastParent = []string{commit.mark}
	}
	for i, event := range rs.repo.events {
		if reset, ok := event.(*Reset); ok && reset.ref == source {
			sourceReset = i
		}
	}
	if sourceReset != -1 {
		repo.delete([]int{sourceReset}, nil)
	}
	repo.declareSequenceMutation("debranch operation")
	return false
}

// HelpPath says "Shut up, golint!"
// FIXME: Odd syntax
func (rs *Reposurgeon) HelpPath() {
	rs.helpOutput(`
path {SOURCE} rename [--force] {TARGET}

Rename a path in every fileop of every selected commit.  The
default selection set is all commits. The first argument is interpreted as a
Go regular expression to match against paths; the second may contain Go
back-reference syntax.

Ordinarily, if the target path already exists in the fileops, or is visible
in the ancestry of the commit, this command throws an error.  With the
--force option, these checks are skipped.
`)
}

type pathAction struct {
	fileop  *FileOp
	commit  *Commit // Only used for debug dump
	attr    string
	newpath string
}

func (pa pathAction) String() string {
	var i int
	var op *FileOp
	for i, op = range pa.commit.fileops {
		if op.Equals(pa.fileop) {
			break
		}
	}

	return fmt.Sprintf("[%s(%d) %s=%s]", pa.commit.idMe(), i, pa.attr, pa.newpath)
}

// DoPath rename paths in the history.
func (rs *Reposurgeon) DoPath(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = repo.all()
	}
	var sourcePattern string
	sourcePattern, line = popToken(line)
	sourceRE, err1 := regexp.Compile(sourcePattern)
	if err1 != nil {
		if logEnable(logWARN) {
			logit("source path regexp compilation failed: %v", err1)
		}
		return false
	}
	var verb string
	verb, line = popToken(line)
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	if verb == "rename" {
		force := parse.options.Contains("--force")
		targetPattern, _ := popToken(parse.line)
		if targetPattern == "" {
			if logEnable(logWARN) {
				logit("no target specified in rename")
			}
			return false
		}
		actions := make([]pathAction, 0)
		for _, commit := range repo.commits(selection) {
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
								return false
							} else if !force && commit.paths(nil).Contains(newpath) {
								if logEnable(logWARN) {
									logit("rename of %s at %s failed, %s exists there", oldpath, commit.idMe(), newpath)
								}
								return false
							} else {
								actions = append(actions, pathAction{fileop, commit, attr, newpath})
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
	} else {
		if logEnable(logWARN) {
			logit("unknown verb '%s' in path command.", verb)
		}
	}
	return false
}

// HelpPaths says "Shut up, golint!"
func (rs *Reposurgeon) HelpPaths() {
	rs.helpOutput(`
paths [sub|sup] [DIRECTORY] [>OUTFILE]

Without a modifier, list all paths touched by fileops in
the selection set (which defaults to the entire repo). This
variant does > redirection.

With the 'sub' modifier, take a second argument that is a directory
name and prepend it to every path. With the 'sup' modifier, strip
any directory argument from the start of the path if it appears there;
with no argument, strip the first directory component from every path.
`)
}

// DoPaths is the command handler for the "paths" command.
func (rs *Reposurgeon) DoPaths(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	if !strings.HasPrefix(line, "sub") && !strings.HasPrefix(line, "sup") {
		allpaths := newOrderedStringSet()
		for _, commit := range rs.chosen().commits(rs.selection) {
			allpaths = allpaths.Union(commit.paths(nil))
		}
		sort.Strings(allpaths)
		fmt.Fprint(parse.stdout, strings.Join(allpaths, control.lineSep)+control.lineSep)
		return false
	}
	fields := strings.Fields(line)
	if fields[0] == "sub" {
		if len(fields) < 2 {
			croak("Error paths sub needs a directory name argument")
			return false
		}
		prefix := fields[1]
		modified := rs.chosen().pathWalk(selection,
			func(f string) string { return prefix + string(os.PathSeparator) + f })
		fmt.Fprint(parse.stdout, strings.Join(modified, control.lineSep)+control.lineSep)
	} else if fields[0] == "sup" {
		if len(fields) == 1 {
			modified := rs.chosen().pathWalk(selection,
				func(f string) string {
					slash := strings.Index(f, "/")
					if slash == -1 {
						return f
					}
					return f[slash+1:]
				})
			sort.Strings(modified)
			fmt.Fprint(parse.stdout, strings.Join(modified, control.lineSep)+control.lineSep)
		} else {
			prefix := fields[1]
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			modified := rs.chosen().pathWalk(selection,
				func(f string) string {
					if strings.HasPrefix(f, prefix) {
						return f[len(prefix):]
					}
					return f
				})
			sort.Strings(modified)
			fmt.Fprint(parse.stdout, strings.Join(modified, control.lineSep)+control.lineSep)
			return false
		}
	}
	//rs.chosen().invalidateManifests()
	return false
}

// HelpManifest says "Shut up, golint!"
func (rs *Reposurgeon) HelpManifest() {
	rs.helpOutput(`
[SELECTION] manifest [/REGEXP/] [>OUTFILE]

Print commit path lists. Takes an optional selection set argument
defaulting to all commits, and an optional delimited Go regular
expression.  For each commit in the selection set, print the mapping
of all paths in that commit tree to the corresponding blob marks,
mirroring what files would be created in a checkout of the commit. If
a regular expression is given, only print "path -> mark" lines for
paths matching it.  This command supports > redirection.
`)
}

// DoManifest prints all files (matching the regex) in the selected commits trees.
func (rs *Reposurgeon) DoManifest(line string) bool {
	if rs.chosen() == nil {
		if logEnable(logWARN) {
			logit("no repo has been chosen")
		}
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	var filterFunc = func(s string) bool { return true }
	line = strings.TrimSpace(parse.line)
	if line != "" {
		if len(line) >= 2 && line[0] != line[len(line)-1] {
			croak("regular expression requires matching start and end delimiters")
			return false
		}
		filterRE, err := regexp.Compile(line[1 : len(line)-1])
		if err != nil {
			if logEnable(logWARN) {
				logit("invalid regular expression: %v", err)
			}
			return false
		}
		filterFunc = func(s string) bool {
			return filterRE.MatchString(s)
		}
	}
	events := rs.chosen().events
	for _, ei := range selection {
		commit, ok := events[ei].(*Commit)
		if !ok {
			continue
		}
		header := fmt.Sprintf("Event %d, ", ei+1)
		header = header[:len(header)-2]
		header += " " + strings.Repeat("=", 72-len(header)) + control.lineSep
		fmt.Fprint(parse.stdout, header)
		if commit.legacyID != "" {
			fmt.Fprintf(parse.stdout, "# Legacy-ID: %s\n", commit.legacyID)
		}
		fmt.Fprintf(parse.stdout, "commit %s\n", commit.Branch)
		if commit.mark != "" {
			fmt.Fprintf(parse.stdout, "mark %s\n", commit.mark)
		}
		fmt.Fprint(parse.stdout, control.lineSep)
		type ManifestItem struct {
			path  string
			entry *FileOp
		}
		manifestItems := make([]ManifestItem, 0)
		commit.manifest().iter(func(path string, pentry interface{}) {
			entry := pentry.(*FileOp)
			if filterFunc(path) {
				manifestItems = append(manifestItems, ManifestItem{path, entry})
			}
		})
		sort.Slice(manifestItems, func(i, j int) bool { return manifestItems[i].path < manifestItems[j].path })
		for _, item := range manifestItems {
			fmt.Fprintf(parse.stdout, "%s -> %s\n", item.path, item.entry.ref)
		}
	}
	return false
}

// HelpTagify says "Shut up, golint!"
func (rs *Reposurgeon) HelpTagify() {
	rs.helpOutput(`
[SELECTION] tagify [--tagify-merges|--canonicalize|--tipdeletes]

Search for empty commits and turn them into tags. Takes an optional selection
set argument defaulting to all commits. For each commit in the selection set,
turn it into a tag with the same message and author information if it has no
fileops. By default merge commits are not considered, even if they have no
fileops (thus no tree differences with their first parent). To change that, see
the '--tagify-merges' option.

The name of the generated tag will be 'emptycommit-<ident>', where <ident>
is generated from the legacy ID of the deleted commit, or from its
mark, or from its index in the repository, with a disambiguation
suffix if needed.

tagify currently recognizes three options: first is '--canonicalize' which
makes tagify try harder to detect trivial commits by first ensuring that all
fileops of selected commits will have an actual effect when processed by
fast-import.

The second option is '--tipdeletes' which makes tagify also consider branch
tips with only deleteall fileops to be candidates for tagification. The
corresponding tags get names of the form 'tipdelete-<branchname>' rather than
the default 'emptycommit-<ident>'.

The third option is '--tagify-merges' that makes reposurgeon also
tagify merge commits that have no fileops.  When this is done the
merge link is moved to the tagified commit's parent.
`)
}

// DoTagify searches for empty commits and turn them into tags.
func (rs *Reposurgeon) DoTagify(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = repo.all()
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	if parse.line != "" {
		croak("too many arguments for tagify.")
		return false
	}
	before := len(repo.commits(nil))
	err := repo.tagifyEmpty(
		selection,
		parse.options.Contains("--tipdeletes"),
		parse.options.Contains("--tagify-merges"),
		parse.options.Contains("--canonicalize"),
		nil,
		nil,
		true)
	if err != nil {
		control.baton.printLogString(err.Error())
	}
	after := len(repo.commits(nil))
	respond("%d commits tagified.", before-after)
	return false
}

// HelpMerge says "Shut up, golint!"
func (rs *Reposurgeon) HelpMerge() {
	rs.helpOutput(`
{SELECTION} merge

Create a merge link. Takes a selection set argument, ignoring all but
the lowest (source) and highest (target) members.  Creates a merge link
from the highest member (child) to the lowest (parent).
`)
}

// DoMerge is the command handler for the "merge" command.
func (rs *Reposurgeon) DoMerge(_line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	commits := rs.chosen().commits(rs.selection)
	if len(commits) < 2 {
		croak("merge requires a selection set with at least two commits.")
		return false
	}
	early := commits[0]
	late := commits[len(commits)-1]
	if late.mark < early.mark {
		late, early = early, late
	}
	late.addParentCommit(early)
	//earlyID = fmt.Sprintf("%s (%s)", early.mark, early.Branch)
	//lateID = fmt.Sprintf("%s (%s)", late.mark, late.Branch)
	//respond("%s added as a parent of %s", earlyID, lateID)
	return false
}

// HelpUnmerge says "Shut up, golint!"
func (rs *Reposurgeon) HelpUnmerge() {
	rs.helpOutput(`
{SELECTION} unmerge

Linearizes a commit. Takes a selection set argument, which must resolve to a
single commit, and removes all its parents except for the first. It is
equivalent to reparent --rebase {first parent},{commit}, where {commit} is the
selection set given to unmerge and {first parent} is a set resolving to that
commit's first parent, but doesn't need you to find the first parent yourself.
`)
}

// DoUnmerge says "Shut up, golint!"
func (rs *Reposurgeon) DoUnmerge(_line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if len(rs.selection) != 1 {
		croak("unmerge requires a single commit.")
		return false
	}
	event := rs.chosen().events[rs.selection[0]]
	if commit, ok := event.(*Commit); !ok {
		croak("unmerge target is not a commit.")
	} else {
		commit.setParents(commit.parents()[:1])
	}
	return false

}

// HelpReparent says "Shut up, golint!"
func (rs *Reposurgeon) HelpReparent() {
	rs.helpOutput(`
{SELECTION} reparent [--user-order] [--rebase]

Changes the parent list of a commit.  Takes a selection set, zero or
more option arguments, and an optional policy argument.

Selection set:

    The selection set must resolve to one or more commits.  The
    selected commit with the highest event number (not necessarily the
    last one selected) is the commit to modify.  The remainder of the
    selected commits, if any, become its parents:  the selected commit
    with the lowest event number (which is not necessarily the first
    one selected) becomes the first parent, the selected commit with
    second lowest event number becomes the second parent, and so on.
    All original parent links are removed.  Examples:

        # this makes 17 the parent of 33
        17,33 reparent

        # this also makes 17 the parent of 33
        33,17 reparent

        # this makes 33 a root (parentless) commit
        33 reparent

        # this makes 33 an octopus merge commit.  its first parent
        # is commit 15, second parent is 17, and third parent is 22
        22,33,15,17 reparent

Options:

    --use-order

        Use the selection order to determine which selected commit is
        the commit to modify and which are the parents (and if there
        are multiple parents, their order).  The last selected commit
        (not necessarily the one with the highest event number) is the
        commit to modify, the first selected commit (not necessarily
        the one with the lowest event number) becomes the first
        parent, the second selected commit becomes the second parent,
        and so on.  Examples:

            # this makes 33 the parent of 17
            33|17 reparent --use-order

            # this makes 17 an octopus merge commit.  its first parent
            # is commit 22, second parent is 33, and third parent is 15
            22,33,15|17 reparent --use-order

        Because ancestor commit events must appear before their
        descendants, giving a commit with a low event number a parent
        with a high event number triggers a re-sort of the events.  A
        re-sort assigns different event numbers to some or all of the
        events.  Re-sorting only works if the reparenting does not
        introduce any cycles.  To swap the order of two commits that
        have an ancestor-descendant relationship without introducing a
        cycle during the process, you must reparent the descendant
        commit first.

    --rebase

	By default, the manifest of the reparented commit is computed
	before modifying it; a 'deleteall' and some fileops are prepended
	so that the manifest stays unchanged even when the first parent
	has been changed.  This behavior can be changed by specifying a
	policy flag:

        Inhibits the default behavior -- no 'deleteall' is issued and
        the tree contents of all descendants can be modified as a
        result.
`)
}

// DoReparent is rthe ommand handler for the "reparent" command.
func (rs *Reposurgeon) DoReparent(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	for _, commit := range repo.commits(nil) {
		commit.invalidateManifests()
	}
	parse := rs.newLineParse(line, nil)
	defer parse.Closem()
	useOrder := parse.options.Contains("--use-order")
	// Determine whether an event resort might be needed.  it is
	// assumed that ancestor commits already have a lower event
	// index before this function is called, which should be true
	// as long as every function that modifies the DAG calls
	// Repository.resort() when needed.  thus, a call to resort()
	// should only be necessary if --use-order is passed and a
	// parent will have an index higher than the modified commit.
	var doResort bool
	if useOrder {
		for _, idx := range rs.selection[:len(rs.selection)-1] {
			if idx > rs.selection[len(rs.selection)-1] {
				doResort = true
			}
		}
	} else {
		sort.Ints(rs.selection)
	}
	selected := repo.commits(rs.selection)
	if len(selected) == 0 || len(rs.selection) != len(selected) {
		if logEnable(logWARN) {
			logit("reparent requires one or more selected commits")
		}
	}
	child := selected[len(selected)-1]
	parents := make([]CommitLike, len(rs.selection)-1)
	for i, c := range selected[:len(selected)-1] {
		parents[i] = c
	}
	if doResort {
		for _, p := range parents {
			if p.(*Commit).descendedFrom(child) {
				if logEnable(logWARN) {
					logit("reparenting a commit to its own descendant would introduce a cycle")
				}
				return false
			}
		}
	}
	if !parse.options.Contains("--rebase") {
		// Recreate the state of the tree
		f := newFileOp(repo)
		f.construct(deleteall)
		newops := []*FileOp{f}
		child.manifest().iter(func(path string, pentry interface{}) {
			entry := pentry.(*FileOp)
			f = newFileOp(repo)
			f.construct(opM, entry.mode, entry.ref, path)
			if entry.ref == "inline" {
				f.inline = entry.inline
			}
			newops = append(newops, f)
		})
		child.setOperations(newops)
		child.simplify()
	}
	child.setParents(parents)
	// Restore this when we have toposort working identically in Go and Python.
	if doResort {
		repo.resort()
	}
	return false
}

// HelpReorder says "Shut up, golint!"
func (rs *Reposurgeon) HelpReorder() {
	rs.helpOutput(`
[SELECTION] reorder [--quiet]

Re-order a contiguous range of commits.

Older revision control systems tracked change history on a per-file basis,
rather than as a series of atomic "changesets", which often made it difficult
to determine the relationships between changes. Some tools which convert a
history from one revision control system to another attempt to infer
changesets by comparing file commit comment and time-stamp against those of
other nearby commits, but such inference is a heuristic and can easily fail.

In the best case, when inference fails, a range of commits in the resulting
conversion which should have been coalesced into a single changeset instead
end up as a contiguous range of separate commits. This situation typically can
be repaired easily enough with the 'coalesce' or 'squash' commands. However,
in the worst case, numerous commits from several different "topics", each of
which should have been one or more distinct changesets, may end up interleaved
in an apparently chaotic fashion. To deal with such cases, the commits need to
be re-ordered, so that those pertaining to each particular topic are clumped
together, and then possibly squashed into one or more changesets pertaining to
each topic. This command, 'reorder', can help with the first task; the
'squash' command with the second.

Selected commits are re-arranged in the order specified; for instance:
":7,:5,:9,:3 reorder". The specified commit range must be contiguous; each
commit must be accounted for after re-ordering. Thus, for example, ':5' can
not be omitted from ":7,:5,:9,:3 reorder". (To drop a commit, use the 'delete'
or 'squash' command.) The selected commits must represent a linear history,
however, the lowest numbered commit being re-ordered may have multiple
parents, and the highest numbered may have multiple children.

Re-ordered commits and their immediate descendants are inspected for
rudimentary fileops inconsistencies. Warns if re-ordering results in a commit
trying to delete, rename, or copy a file before it was ever created. Likewise,
warns if all of a commit's fileops become no-ops after re-ordering. Other
fileops inconsistencies may arise from re-ordering, both within the range of
affected commits and beyond; for instance, moving a commit which renames a
file ahead of a commit which references the original name. Such anomalies can
be discovered via manual inspection and repaired with the 'add' and 'remove'
(and possibly 'path') commands. Warnings can be suppressed with '--quiet'.

In addition to adjusting their parent/child relationships, re-ordering commits
also re-orders the underlying events since ancestors must appear before
descendants, and blobs must appear before commits which reference them. This
means that events within the specified range will have different event numbers
after the operation.
`)
}

// DoReorder re-orders a contiguous range of commits.
func (rs *Reposurgeon) DoReorder(lineIn string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	sel := rs.selection
	if sel == nil {
		croak("no selection")
		return false
	}
	parse := rs.newLineParse(lineIn, nil)
	defer parse.Closem()
	if parse.line != "" {
		croak("'reorder' takes no arguments")
		return false
	}
	commits := repo.commits(sel)
	if len(commits) == 0 {
		croak("no commits in selection")
		return false
	} else if len(commits) == 1 {
		croak("only 1 commit selected; nothing to re-order")
		return false
	} else if len(commits) != len(sel) {
		croak("selection set must be all commits")
		return false
	}
	_, quiet := parse.OptVal("--quiet")

	repo.reorderCommits(sel, quiet)
	return false
}

// HelpBranch says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranch() {
	rs.helpOutput(`
branch {BRANCH-NAME|/PATTERN/} {rename|delete} [ARG]

Rename or delete a branch (and any associated resets).  First argument
must be an existing branch name; second argument must one of the verbs
'rename' or 'delete'.

For a 'rename', the third argument may be any token that is a syntactically
valid branch name (but not the name of an existing branch).  If it does not
contain a '/' the prefix 'heads/' is prepended.  If it does not begin with
'refs/', then 'refs/' is prepended.

For a 'delete', the name may optionally be a regular expression wrapped in //;
if so, all objects of the specified type with names matching the regexp are
deleted.  This is useful for mass deletion of branches.  Such deletions can be
restricted by a selection set in the normal way.  No third argument is
required.`)
}

func branchNameMatches(name string, regex *regexp.Regexp) bool {
	return strings.HasPrefix(name, "refs/heads/") && regex.MatchString(name[11:])
}

func tagNameMatches(name string, regex *regexp.Regexp) bool {
	return strings.HasPrefix(name, "refs/tags/") && regex.MatchString(name[10:])
}

// DoBranch renames a branch or deletes it.
func (rs *Reposurgeon) DoBranch(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	branchname, line := popToken(line)
	var err error
	branchname, err = stringEscape(branchname)
	if err != nil {
		croak("while selecting branch: %v", err)
		return false
	}
	var verb string
	verb, line = popToken(line)
	if verb == "rename" {
		if !strings.Contains(branchname, "/") {
			branchname = "refs/heads/" + branchname
		}
		if !repo.branchset().Contains(branchname) {
			croak("no such branch as %s", branchname)
			return false
		}
		var newname string
		newname, line = popToken(line)
		if newname == "" {
			croak("new branch name must be nonempty.")
			return false
		}
		if !strings.Contains(newname, "/") {
			newname = "refs/heads/" + newname
		}
		if repo.branchset().Contains(newname) {
			croak("there is already a branch named '%s'.", newname)
			return false
		}
		for _, event := range repo.events {
			if commit, ok := event.(*Commit); ok {
				if commit.Branch == branchname {
					commit.setBranch(newname)
				}
			} else if reset, ok := event.(*Reset); ok {
				if reset.ref == branchname {
					reset.ref = newname
				}
			}
		}
	} else if verb == "delete" {
		selection := rs.selection
		if selection == nil {
			selection = repo.all()
		}
		var shouldDelete func(string) bool
		if branchname[0] == '/' && branchname[len(branchname)-1] == '/' {
			// Regexp - can refer to a list of branchs matched
			branchre, err := regexp.Compile(branchname[1 : len(branchname)-1])
			if err != nil {
				croak("in branch command: %v", err)
				return false
			}
			shouldDelete = func(branch string) bool {
				return branchNameMatches(branch, branchre)
			}
		} else {
			theref := "refs/heads/" + branchname
			if !repo.branchset().Contains(theref) {
				croak("no such branch as %s", branchname)
				return false
			}
			shouldDelete = func(branch string) bool {
				return branch == theref
			}
		}
		repo.deleteBranch(selection, shouldDelete)
	} else {
		croak("unknown verb '%s' in branch command.", verb)
		return false
	}
	return false
}

// HelpTag says "Shut up, golint!"
func (rs *Reposurgeon) HelpTag() {
	rs.helpOutput(`
[SELECTION] tag {TAG-NAME} {create|move|rename|delete} [ARG]

Create, move, rename, or delete a tag.

Creation is a special case.  First argument is a name, which must not
be an existing tag. Takes a singleton event second argument which must
point to a commit.  A tag object pointing to the commit is created and
inserted just after the last tag in the repo (or just after the last
commit if there are no tags).  The tagger, committish, and comment
fields are copied from the commit's committer, mark, and comment
fields.

Otherwise, the first argument must be an existing name referring to a
tag object, lightweight tag, or reset; second argument must be one of
the verbs 'move', 'rename', or 'delete'.

For a 'move', a third argument must be a singleton selection set. For
a 'rename', the third argument may be any token that is a
syntactically valid tag name (but not the name of an existing
tag).

For a 'delete', no third argument is required.  The name portion of a
delete may be a regexp wrapped in //; if so, all objects of the
specified type with names matching the regexp are deleted.  This is
useful for mass deletion of junk tags such as CVS branch-root tags.
Such deletions can be restricted by a selection set in the normal way.

Tag names may use backslash escapes interpreted by the Python
string-escape codec, such as \s.

The behavior of this command is complex because features which present
as tags may be any of three things: (1) true tag objects, (2)
lightweight tags, actually sequences of commits with a common
branchname beginning with 'refs/tags' - in this case the tag is
considered to point to the last commit in the sequence, (3) Reset
objects.  These may occur in combination; in fact, stream exporters
from systems with annotation tags commonly express each of these as a
true tag object (1) pointing at the tip commit of a sequence (2) in
which the basename of the common branch field is identical to the tag
name.  An exporter that generates lightweight-tagged commit sequences (2)
may or may not generate resets pointing at their tip commits.

This command tries to handle all combinations in a natural way by
doing up to three operations on any true tag, commit sequence, and
reset matching the source name. In a rename, all are renamed together.
In a delete, any matching tag or reset is deleted; then matching
branch fields are changed to match the branch of the unique descendant
of the tagged commit, if there is one.  When a tag is moved, no branch
fields are changed and a warning is issued.
`)
}

// DoTag moves a tag to point to a specified commit, or renames it, or deletes it.
func (rs *Reposurgeon) DoTag(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	// A tag name can refer to one of the following things {
	// (1) A tag object, by name
	// (2) A reset object having a name in the tags/ namespace
	// (3) The tip commit of a branch with branch fields
	// These things often occur in combination. Notably, git-fast-export
	// generates for each tag object corresponding branch labels on
	// some ancestor commits - the rule for where this stops is unclear.
	var tagname string
	tagname, line = popToken(line)
	if len(tagname) == 0 {
		croak("missing tag name")
		return false
	}
	var err error
	tagname, err = stringEscape(tagname)
	if err != nil {
		croak("in tag command: %v", err)
		return false
	}
	var verb string
	verb, line = popToken(line)
	if verb == "create" {
		var ok bool
		var target *Commit
		if repo.named(tagname) != nil {
			croak("something is already named %s", tagname)
			return false
		}
		rs.setSelectionSet(line)
		if rs.selection == nil {
			croak("usage: tag <name> create <singleton-selection>")
			return false
		} else if len(rs.selection) != 1 {
			croak("tag create requires a singleton commit set.")
			return false
		} else if target, ok = repo.events[rs.selection[0]].(*Commit); !ok {
			croak("create target is not a commit.")
			return false
		}
		tag := newTag(repo, tagname, target.mark,
			target.committer.clone(),
			target.Comment)
		tag.tagger.date.timestamp = tag.tagger.date.timestamp.Add(time.Second) // So it is unique
		var lasttag int
		var lastcommit int
		for i, event := range repo.events {
			if _, ok := event.(*Tag); ok {
				lasttag = i
			} else if _, ok := event.(*Commit); ok {
				lastcommit = i
			}
			control.baton.twirl()
		}
		if lasttag == 0 {
			lasttag = lastcommit
		}
		repo.insertEvent(tag, lasttag+1, "tag creation")
		control.baton.twirl()
		return false
	}
	tags := make([]*Tag, 0)
	resets := make([]*Reset, 0)
	commits := make([]*Commit, 0)
	var refMatches func(string) bool
	if tagname[0] == '/' && tagname[len(tagname)-1] == '/' {
		// Regexp - can refer to a list of tags matched
		tagre, err := regexp.Compile(tagname[1 : len(tagname)-1])
		if err != nil {
			croak("in tag command: %v", err)
			return false
		}
		refMatches = func(branch string) bool {
			return tagNameMatches(branch, tagre)
		}
	} else {
		// Non-regexp - can only refer to a single tag
		fulltagname := branchname(tagname)
		refMatches = func(branch string) bool {
			return branch == fulltagname
		}
	}
	selection := rs.selection
	if selection == nil {
		selection = repo.all()
	}
	for _, idx := range selection {
		event := repo.events[idx]
		if tag, ok := event.(*Tag); ok && refMatches(tag.name) {
			tags = append(tags, tag)
		} else if reset, ok := event.(*Reset); ok && refMatches(reset.ref) {
			resets = append(resets, reset)
		} else if commit, ok := event.(*Commit); ok && refMatches(commit.Branch) {
			commits = append(commits, commit)
		}
		control.baton.twirl()
	}
	if len(tags) == 0 && len(resets) == 0 && len(commits) == 0 {
		croak("no tags matching %s", tagname)
		return false
	}
	if verb == "move" {
		var target *Commit
		var ok bool
		rs.setSelectionSet(line)
		if len(rs.selection) != 1 {
			croak("tag move requires a singleton commit set.")
			return false
		} else if target, ok = repo.events[rs.selection[0]].(*Commit); !ok {
			croak("move target is not a commit.")
			return false
		}
		if len(tags) > 0 {
			for _, tag := range tags {
				tag.forget()
				tag.remember(repo, target.mark)
				control.baton.twirl()
			}
		}
		if len(resets) > 0 {
			if len(resets) == 1 {
				resets[0].committish = target.mark
			} else {
				croak("cannot move multiple tags.")
			}
			control.baton.twirl()
		}
		if len(commits) > 0 {
			// Delete everything only reachable from the old tag position,
			// and change the Branch of every commit that happened on that
			// old tag but is still reachable from elsewhere.
			repo.deleteBranch(selection, refMatches)
		}
	} else if verb == "rename" {
		if len(tags) > 1 {
			croak("exactly one tag is required for rename")
			return false
		}
		var newname string
		newname, line = popToken(line)
		if newname == "" {
			croak("new tag name must be nonempty.")
			return false
		}
		if len(tags) == 1 {
			for _, event := range repo.events {
				if tag, ok := event.(*Tag); ok && tag != tags[0] && tag.name == tags[0].name {
					croak("tag name collision, not renaming.")
					return false
				}
			}
			tags[0].setHumanName(newname)

			control.baton.twirl()
		}
		fullnewname := branchname(newname)
		for _, reset := range resets {
			reset.ref = fullnewname
		}
		for _, event := range commits {
			event.Branch = fullnewname
		}
	} else if verb == "delete" {
		for _, tag := range tags {
			// the order here in important
			repo.delete([]int{tag.index()}, nil)
			tag.forget()
			control.baton.twirl()
		}
		if len(tags) > 0 {
			repo.declareSequenceMutation("tag deletion")
		}
		for _, reset := range resets {
			reset.forget()
			repo.delete([]int{repo.eventToIndex(reset)}, nil)
		}
		if len(resets) > 0 {
			repo.declareSequenceMutation("reset deletion")
		}
		if len(commits) > 0 {
			repo.deleteBranch(selection, refMatches)
		}
	} else {
		croak("unknown verb '%s' in tag command.", verb)
		return false
	}
	return false
}

// HelpReset says "Shut up, golint!"
func (rs *Reposurgeon) HelpReset() {
	rs.helpOutput(`
[SELECTION] reset {RESET-NAME} {create|move|rename|delete} [ARG]

Create, move, rename, or delete a reset. Create is a special case; it
requires a singleton selection which is the associated commit for the
reset, takes as a first argument the name of the reset (which must not
exist), and ends with the keyword create.

In the other modes, the first argument must match an existing reset
name with the selection; second argument must be one of the verbs
'move', 'rename', or 'delete'. The default selection is all events.

For a 'move', a third argument must be a singleton selection set. For
a 'rename', the third argument may be any token that can be interpreted
as a valid reset name (but not the name of an existing
reset). For a 'delete', no third argument is required.

Reset names may use backslash escapes interpreted by the Go
string-escape codec, such as \s.

An argument matches a reset's name if it is either the entire
reference (refs/heads/FOO or refs/tags/FOO for some some value of FOO)
or the basename (e.g. FOO), or a suffix of the form heads/FOO or tags/FOO.
An unqualified basename is assumed to refer to a head.

When a reset is renamed, commit branch fields matching the tag are
renamed with it to match.  When a reset is deleted, matching branch
fields are changed to match the branch of the unique descendant of the
tip commit of the associated branch, if there is one.  When a reset is
moved, no branch fields are changed.
`)
}

// DoReset moves a reset to point to a specified commit, or renames it, or deletes it.
func (rs *Reposurgeon) DoReset(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	var resetname string
	var err error
	resetname, line = popToken(line)
	resetname, err = stringEscape(resetname)
	if err != nil {
		croak("in reset command: %v", err)
		return false
	}
	if !strings.Contains(resetname, "/") {
		resetname = "heads/" + resetname
	}
	if !strings.HasPrefix(resetname, "refs/") {
		resetname = "refs/" + resetname
	}
	resets := make([]*Reset, 0)
	selection := rs.selection
	if selection == nil {
		selection = rs.repo.all()
	}
	for _, ei := range selection {
		reset, ok := repo.events[ei].(*Reset)
		if ok && reset.ref == resetname {
			resets = append(resets, reset)
		}
	}
	var verb string
	verb, line = popToken(line)
	if verb == "create" {
		var target *Commit
		var ok bool
		if len(resets) > 0 {
			croak("one or more resets match %s", resetname)
			return false
		}
		if len(rs.selection) != 1 {
			croak("reset create requires a singleton commit set.")
			return false
		} else if target, ok = repo.events[rs.selection[0]].(*Commit); !ok {
			croak("create target is not a commit.")
			return false
		}
		reset := newReset(repo, resetname, target.mark, target.legacyID)
		repo.addEvent(reset)
		repo.declareSequenceMutation("reset create")
	} else if verb == "move" {
		var reset *Reset
		var target *Commit
		var ok bool
		if len(resets) == 0 {
			croak("no such reset as %s", resetname)
		}
		if len(resets) == 1 {
			reset = resets[0]
		} else {
			croak("can't move multiple resets")
			return false
		}
		rs.setSelectionSet(line)
		if len(rs.selection) != 1 {
			croak("reset move requires a singleton commit set.")
			return false
		} else if target, ok = repo.events[rs.selection[0]].(*Commit); !ok {
			croak("move target is not a commit.")
			return false
		}
		reset.forget()
		reset.remember(repo, target.mark)
		repo.declareSequenceMutation("reset move")
	} else if verb == "rename" {
		var newname string
		if len(resets) == 0 {
			croak("no such reset as %s", resetname)
			return false
		}
		newname, line = popToken(line)
		if newname == "" {
			croak("new reset name must be nonempty.")
			return false
		}
		if strings.Count(newname, "/") == 0 {
			newname = "heads/" + newname
		}
		if !strings.HasPrefix(newname, "refs/") {
			newname = "refs/" + newname
		}
		selection := rs.selection
		if selection == nil {
			selection = repo.all()
		}
		for _, ei := range selection {
			reset, ok := repo.events[ei].(*Reset)
			if ok && reset.ref == newname {
				croak("reset reference collision, not renaming.")
				return false
			}
		}
		for _, commit := range repo.commits(nil) {
			if commit.Branch == newname {
				croak("commit branch collision, not renaming.")
				return false
			}
		}

		for _, reset := range resets {
			reset.ref = newname
		}
		for _, commit := range repo.commits(nil) {
			if commit.Branch == resetname {
				commit.Branch = newname
			}
		}
	} else if verb == "delete" {
		if len(resets) == 0 {
			croak("no such reset as %s", resetname)
			return false
		}
		var tip *Commit
		for _, commit := range repo.commits(nil) {
			if commit.Branch == resetname {
				tip = commit
			}
		}
		if tip != nil && len(tip.children()) == 1 {
			successor := tip.children()[0]
			if cSuccessor, ok := successor.(*Commit); ok {
				for _, commit := range repo.commits(nil) {
					if commit.Branch == resetname {
						commit.Branch = cSuccessor.Branch
					}
				}
			}
		}
		for _, reset := range resets {
			reset.forget()
			repo.delete([]int{repo.eventToIndex(reset)}, nil)
		}
		repo.declareSequenceMutation("reset delete")
	} else {
		croak("unknown verb '%s' in reset command.", verb)
	}
	return false
}

// HelpIgnores says "Shut up, golint!"
func (rs *Reposurgeon) HelpIgnores() {
	rs.helpOutput(`
ignores [--rename] [--translate] [--defaults]

Intelligent handling of ignore-pattern files.

This command fails if no repository has been selected or no preferred write
type has been set for the repository.  It does not take a selection set.

If --rename is present, this command attempts to rename all
ignore-pattern files to whatever is appropriate for the preferred type
- e.g. .gitignore for git, .hgignore for hg, etc.  This option does
not cause any translation of the ignore files it renames.

If --translate is present, syntax translation of each ignore file is
attempted. At present, the only transformation the code knows is to
prepend a 'syntax: glob' header if the preferred type is hg.

If --defaults is present, the command attempts to prepend these
default patterns to all ignore files. If no ignore file is created by
the first commit, it will be modified to create one containing the
defaults.  This command will error out on prefer types that have no
default ignore patterns (git and hg, in particular).  It will also
error out when it knows the import tool has already set default
patterns.
`)
}

// DoIgnores manipulates ignore patterns in the repo.
func (rs *Reposurgeon) DoIgnores(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	if rs.preferred != nil && rs.ignorename == "" {
		rs.ignorename = rs.preferred.ignorename
	}
	if rs.preferred == nil {
		croak("preferred repository type has not been set")
		return false
	}
	if rs.ignorename == "" {
		croak("preferred repository type has no declared ignorename")
		return false
	}
	isIgnore := func(blob *Blob) bool {
		if len(blob.opset) == 0 {
			return false
		}
		for fop := range blob.opset {
			if !strings.HasSuffix(fop.Path, rs.ignorename) {
				return false
			}
		}
		return true
	}
	for _, verb := range strings.Fields(line) {
		if verb == "--defaults" {
			if rs.preferred.styleflags.Contains("import-defaults") {
				croak("importer already set default ignores")
				return false
			} else if len(rs.preferred.dfltignores) == 0 {
				croak("no default ignores in %s", rs.preferred.name)
				return false
			} else {
				changecount := 0
				// Modify existing ignore files
				for _, event := range repo.events {
					if blob, ok := event.(*Blob); ok && isIgnore(blob) {
						blob.setContent([]byte(rs.preferred.dfltignores+string(blob.getContent())), -1)
						changecount++
					}
				}
				// Create an early ignore file if required.
				// Do not move this before the modification pass!
				earliest := repo.earliestCommit()
				hasIgnoreBlob := false
				for _, fileop := range earliest.operations() {
					if fileop.op == opM && strings.HasSuffix(fileop.Path, rs.ignorename) {
						hasIgnoreBlob = true
					}
				}
				if !hasIgnoreBlob {
					blob := newBlob(repo)
					blob.setContent([]byte(rs.preferred.dfltignores), noOffset)
					blob.mark = ":insert"
					repo.insertEvent(blob, repo.eventToIndex(earliest), "ignore-blob creation")
					repo.declareSequenceMutation("ignore creation")
					newop := newFileOp(rs.chosen())
					newop.construct(opM, "100644", ":insert", rs.ignorename)
					earliest.appendOperation(newop)
					repo.renumber(1, nil)
					respond(fmt.Sprintf("initial %s created.", rs.ignorename))
				}
				respond(fmt.Sprintf("%d %s blobs modified.", changecount, rs.ignorename))
			}
		} else if verb == "--rename" {
			changecount := 0
			for _, commit := range repo.commits(nil) {
				for idx, fileop := range commit.operations() {
					for _, attr := range []string{"Path", "Source", "Target"} {
						oldpath, ok := getAttr(fileop, attr)
						if ok {
							if ok && strings.HasSuffix(oldpath, rs.ignorename) {
								newpath := filepath.Join(filepath.Dir(oldpath),
									rs.preferred.ignorename)
								setAttr(commit.fileops[idx], attr, newpath)
								changecount++
							}
						}
					}
				}
			}
			respond("%d ignore files renamed (%s -> %s).",
				changecount, rs.ignorename, rs.preferred.ignorename)
			rs.ignorename = rs.preferred.ignorename
		} else if verb == "--translate" {
			changecount := 0
			for _, event := range repo.events {
				if blob, ok := event.(*Blob); ok && isIgnore(blob) {
					if rs.preferred.name == "hg" {
						if !bytes.HasPrefix(blob.getContent(), []byte("syntax: glob\n")) {
							blob.setContent([]byte("syntax: glob\n"+string(blob.getContent())), noOffset)
							changecount++
						}
					}
				}
			}
			respond(fmt.Sprintf("%d %s blobs modified.", changecount, rs.ignorename))
		} else {
			croak("unknown option %s in ignores line", verb)
			return false
		}
	}
	return false
}

// HelpAttribution says "Shut up, golint!"
//FIXME: Odd syntax
func (rs *Reposurgeon) HelpAttribution() {
	rs.helpOutput(`
[SELECTION] attribution {SUBCOMMAND}

Inspect, modify, add, and remove commit and tag attributions.

Attributions upon which to operate are selected in much the same way as events
are selected. The ATTR-SELECTION argument of each action is an expression
composed of 1-origin attribution-sequence numbers, '$' for last attribution,
'..' ranges, comma-separated items, '(...)' grouping, set operations '|'
union, '&' intersection, and '~' negation, and function calls @min(), @max(),
@amp(), @pre(), @suc(), @srt().

Attributions can also be selected by visibility set '=C' for committers, '=A'
for authors, and '=T' for taggers.

Finally, /regex/ will attempt to match the Go regular expression regex
against an attribution name and email address; '/n' limits the match to only
the name, and '/e' to only the email address.

With the exception of 'show', all actions require an explicit event selection
upon which to operate.

Available actions are:

[SELECTION] attribution [ATTR-SELECTION] [show] [>file]
    Inspect the selected attributions of the specified events (commits and
    tags). The 'show' keyword is optional. If no attribution selection
    expression is given, defaults to all attributions. If no event selection
    is specified, defaults to all events. Supports > redirection.

{SELECTION} attribution {ATTR-SELECTION} set {NAME} [EMAIL] [DATE]
{SELECTION} attribution {ATTR-SELECTION} set [NAME] {EMAIL} [DATE]
{SELECTION} attribution {ATTR-SELECTION} set [NAME] [EMAIL] {DATE}
    Assign NAME, EMAIL, DATE to the selected attributions. As a
    convenience, if only some fields need to be changed, the others can be
    omitted. Arguments NAME, EMAIL, and DATE can be given in any order.

{SELECTION} attribution delete
    Delete the selected attributions. As a convenience, deletes all authors if
    <selection> is not given. It is an error to delete the mandatory committer
    and tagger attributions of commit and tag events, respectively.

{SELECTION} attribution [ATTR-SELECTION] prepend {NAME} [EMAIL] [DATE]
{SELECTION} attribution [ATTR-SELECTION] prepend [NAME] {EMAIL} [DATE]
    Insert a new attribution before the first attribution named by SELECTION.
    The new attribution has the same type ('committer', 'author', or 'tagger')
    as the one before which it is being inserted. Arguments NAME, EMAIL,
    and DATE can be given in any order.

    If NAME is omitted, an attempt is made to infer it from EMAIL by
    trying to match EMAIL against an existing attribution of the event, with
    preference given to the attribution before which the new attribution is
    being inserted. Similarly, EMAIL is inferred from an existing matching
    NAME. Likewise, for DATE.

    As a convenience, if ATTR-SELECTION is empty or not specified a new author is
    prepended to the author list.

    It is presently an error to insert a new committer or tagger attribution.
    To change a committer or tagger, use 'set' instead.

{SELECTION} attribution [ATTR-SELECTION] append {NAME} [EMAIL] [DATE]
{SELECTION} attribution [ATTR-SELECTION] append [NAME] {EMAIL} [DATE]
    Insert a new attribution after the last attribution named by SELECTION.
    The new attribution has the same type ('committer', 'author', or 'tagger')
    as the one after which it is being inserted. Arguments NAME, EMAIL,
    and DATE can be given in any order.

    If NAME is omitted, an attempt is made to infer it from EMAIL by
    trying to match EMAIL against an existing attribution of the event, with
    preference given to the attribution after which the new attribution is
    being inserted. Similarly, EMAIL is inferred from an existing matching
    NAME. Likewise, for DATE.

    As a convenience, if SELECTION is empty or not specified a new author is
    appended to the author list.

    It is presently an error to insert a new committer or tagger attribution.
    To change a committer or tagger, use 'set' instead.

{SELECTION} attribution {ATTR-SELECTION} resolve [>file] [LABEL-TEXT...]
    Does nothing but resolve an attribution selection-set expression for the
    selected events and echo the resulting attribution-number set to standard
    output. The remainder of the line after the command is used as a label for
    the output.

    Implemented mainly for regression testing, but may be useful for exploring
    the selection-set language.
`)
}

// DoAttribution inspects, modifies, adds, and removes commit and tag attributions.
func (rs *Reposurgeon) DoAttribution(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen")
		return false
	}
	selparser := newAttrEditSelParser()
	machine, rest := selparser.compile(line)
	parse := rs.newLineParse(rest, orderedStringSet{"stdout"})
	defer parse.Closem()
	fields, err := shlex.Split(parse.line, true)
	if err != nil {
		croak("attribution parse failed: %v", err)
		return false
	}
	var action string
	args := []string{}
	if len(fields) == 0 {
		action = "show"
	} else {
		action = fields[0]
		args = fields[1:]
	}
	selection := rs.selection
	if rs.selection == nil {
		if action == "show" {
			selection = repo.all()
		} else {
			croak("no selection")
			return false
		}
	}
	var sel []int
	for _, i := range selection {
		switch repo.events[i].(type) {
		case *Commit, *Tag:
			sel = append(sel, i)
		}
	}
	if len(sel) == 0 {
		croak("no commits or tags in selection")
		return false
	}
	ed := newAttributionEditor(sel, repo.events, func(attrs []attrEditAttr) []int {
		state := selparser.evalState(attrs)
		defer state.release()
		return selparser.evaluate(machine, state)
	})
	if action == "show" {
		if len(args) > 0 {
			croak("'show' takes no arguments")
			return false
		}
		ed.inspect(parse.stdout)
	} else if action == "delete" {
		if len(args) > 0 {
			croak("'delete' takes no arguments")
			return false
		}
		ed.remove()
	} else if action == "set" {
		if len(args) < 1 || len(args) > 3 {
			croak("'set' requires at least one of: name, email, date")
			return false
		}
		ed.assign(args)
	} else if action == "prepend" || action == "append" {
		if len(args) < 1 || len(args) > 3 {
			croak("'%s' requires at least one of: name, email; date is optional", action)
			return false
		}
		if action == "prepend" {
			ed.insert(args, false)
		} else if action == "append" {
			ed.insert(args, true)
		}
	} else if action == "resolve" {
		ed.resolve(parse.stdout, strings.Join(args, " "))
	} else {
		croak("unrecognized action: %s", action)
		return false
	}
	return false
}

//
// Artifact removal
//

// HelpAuthors says "Shut up, golint!"
func (rs *Reposurgeon) HelpAuthors() {
	rs.helpOutput(`
authors read {<INFILE}

authors write {>OUTFILE}

Apply or dump author-map information for the specified selection
set, defaulting to all events.

Lifts from CVS and Subversion may have only usernames local to
the repository host in committer and author IDs. DVCSes want email
addresses (net-wide identifiers) and complete names. To supply the map
from one to the other, an authors file is expected to consist of
lines each beginning with a local user ID, followed by a '=' (possibly
surrounded by whitespace) followed by a full name and email address.

When an authors file is applied, email addresses in committer and author
metdata for which the local ID matches between &lt; and @ are replaced
according to the mapping (this handles git-svn lifts). Alternatively,
if the local ID is the entire address, this is also considered a match
(this handles what git-cvsimport and cvs2git do). If a timezone was
specified in the map entry, that person's author and committer dates
are mapped to it.

With the 'read' modifier, or no modifier, apply author mapping data
(from standard input or a <-redirected input file).  May be useful if
you are editing a repo or dump created by cvs2git or by
cvs-fast-export or git-svn invoked without -A.

With the 'write' modifier, write a mapping file that could be
interpreted by 'authors read', with entries for each unique committer,
author, and tagger (to standard output or a >-redirected file). This
may be helpful as a start on building an authors file, though each
part to the right of an equals sign will need editing.
`)
}

// DoAuthors applies or dumps author-mapping file.
func (rs *Reposurgeon) DoAuthors(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	if strings.HasPrefix(line, "write") {
		line = strings.TrimSpace(line[5:])
		parse := rs.newLineParse(line, orderedStringSet{"stdout"})
		defer parse.Closem()
		if len(parse.Tokens()) > 0 {
			croak("authors write no longer takes a filename argument - use > redirection instead")
			return false
		}
		rs.chosen().writeAuthorMap(selection, parse.stdout)
	} else {
		if strings.HasPrefix(line, "read") {
			line = strings.TrimSpace(line[4:])
		}
		parse := rs.newLineParse(line, orderedStringSet{"stdin"})
		defer parse.Closem()
		if len(parse.Tokens()) > 0 {
			croak("authors read no longer takes a filename argument - use < redirection instead")
			return false
		}
		rs.chosen().readAuthorMap(selection, parse.stdin)
	}
	return false
}

//
// Reference lifting
//

// HelpLegacy says "Shut up, golint!"
func (rs *Reposurgeon) HelpLegacy() {
	rs.helpOutput(`
legacy read [<INFILE]

legacy write [>OUTFILE]

Apply or list legacy-reference information. Does not take a
selection set. The 'read' variant reads from standard input or a
<-redirected filename; the 'write' variant writes to standard
output or a >-redirected filename.
`)
}

// DoLegacy apply a reference-mapping file.
func (rs *Reposurgeon) DoLegacy(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	if strings.HasPrefix(line, "write") {
		line = strings.TrimSpace(line[5:])
		parse := rs.newLineParse(line, orderedStringSet{"stdout"})
		defer parse.Closem()
		if len(parse.Tokens()) > 0 {
			croak("legacy write does not take a filename argument - use > redirection instead")
			return false
		}
		rs.chosen().writeLegacyMap(parse.stdout)
	} else {
		if strings.HasPrefix(line, "read") {
			line = strings.TrimSpace(line[4:])
		}
		parse := rs.newLineParse(line, []string{"stdin"})
		defer parse.Closem()
		if len(parse.Tokens()) > 0 {
			croak("legacy read does not take a filename argument - use < redirection instead")
			return false
		}
		rs.chosen().readLegacyMap(parse.stdin)
	}
	return false
}

// HelpReferences says "Shut up, golint!"
// FIXME: Odd syntax
func (rs *Reposurgeon) HelpReferences() {
	rs.helpOutput(`
[SELECTION] references [list|edit|lift]

With the 'list' modifier, produces a listing of events that may have
Subversion or CVS commit references in them.  This version
of the command supports >-redirection.  Equivalent to '=N list'.

With the modifier 'edit', edit this set.  This version of the command
supports < and > redirection.  Equivalent to '=N edit'.

With the modifier 'lift', transform commit-reference cookies from CVS
and Subversion into action stamps.  This command expects cookies
consisting of the leading string '[[', followed by a VCS identifier
(currently SVN or CVS) followed by VCS-dependent information, followed
by ']]'. An action stamp pointing at the corresponding commit is
substituted when possible.  Enables writing of the legacy-reference
map when the repo is written or rebuilt.
`)
}

// DoReferences looks for things that might be CVS or Subversion revision references.
func (rs *Reposurgeon) DoReferences(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	if strings.Contains(line, "lift") {
		rs.chosen().parseDollarCookies()
		hits := 0
		substitute := func(getter func(string) *Commit, legend string) string {
			// legend was matchobj.group(0) in Python
			commit := getter(legend)
			if commit == nil {
				if logEnable(logWARN) {
					logit("no commit matches %q", legend)
				}
				return legend // no replacement
			}
			text := commit.actionStamp()
			hits++
			return text
		}
		type getterPair struct {
			pattern string
			getter  func(string) *Commit
		}
		getterPairs := []getterPair{
			{`\[\[CVS:[^:\]]+:[0-9.]+\]\]`,
				func(p string) *Commit {
					p = p[2 : len(p)-2]
					if c := repo.legacyMap[p]; c != nil {
						return c
					}
					c, ok := repo.dollarMap.Load(p)
					if ok {
						return c.(*Commit)
					}
					return nil
				}},
			{`\[\[SVN:[0-9]+\]\]`,
				func(p string) *Commit {
					p = p[2 : len(p)-2]
					if c := repo.legacyMap[p]; c != nil {
						return c
					}
					c, ok := repo.dollarMap.Load(p)
					if ok {
						return c.(*Commit)
					}
					return nil
				}},
			{`\[\[HG:[0-9a-f]+\]\]`,
				func(p string) *Commit {
					p = p[2 : len(p)-2]
					return repo.legacyMap[p]
				}},
			{`\[\[:[0-9]+\]\]`,
				func(p string) *Commit {
					p = p[2 : len(p)-2]
					event := repo.markToEvent(p)
					commit, ok := event.(*Commit)
					if ok {
						return commit
					}
					return nil
				}},
		}
		for _, item := range getterPairs {
			matchRE := regexp.MustCompile(item.pattern)
			for _, commit := range rs.chosen().commits(selection) {
				commit.Comment = matchRE.ReplaceAllStringFunc(
					commit.Comment,
					func(m string) string {
						return substitute(item.getter, m)
					})
			}
		}
		respond("%d references resolved.", hits)
		repo.writeLegacy = true
	} else {
		selection = make([]int, 0)
		for idx, commit := range repo.commits(nil) {
			if rs.hasReference(commit) {
				selection = append(selection, idx)
			}
		}
		if len(selection) > 0 {
			if strings.HasPrefix(line, "edit") {
				rs.edit(selection, strings.TrimSpace(line[4:]))
			} else {
				parse := rs.newLineParse(line, orderedStringSet{"stdout"})
				defer parse.Closem()
				w := screenwidth()
				for _, ei := range selection {
					event := repo.events[ei]
					summary := ""
					switch event.(type) {
					case *Commit:
						commit := event.(*Commit)
						summary = commit.lister(nil, ei, w)
						break
						//case *Tag:
						//	tag := event.(*Tag)
						//	summary = tag.lister(nil, ei, w)
					}
					if summary != "" {
						fmt.Fprint(parse.stdout, summary+control.lineSep)
					}
				}
			}
		}
	}
	return false
}

// HelpGitify says "Shut up, golint!"
func (rs *Reposurgeon) HelpGitify() {
	rs.helpOutput(`
[SELECTION] gitify

Attempt to massage comments into a git-friendly form with a blank
separator line after a summary line.  This code assumes it can insert
a blank line if the first line of the comment ends with '.', ',', ':',
';', '?', or '!'.  If the separator line is already present, the comment
won't be touched.

Takes a selection set, defaulting to all commits and tags.
`)
}

// DoGitify canonicalizes comments.
func (rs *Reposurgeon) DoGitify(_line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	lineEnders := orderedStringSet{".", ",", ";", ":", "?", "!"}
	control.baton.startProgress("gitifying comments", uint64(len(selection)))
	rs.chosen().walkEvents(selection, func(idx int, event Event) {
		if commit, ok := event.(*Commit); ok {
			commit.Comment = canonicalizeComment(commit.Comment)
			if strings.Count(commit.Comment, "\n") < 2 {
				return
			}
			firsteol := strings.Index(commit.Comment, "\n")
			if commit.Comment[firsteol+1] == byte('\n') {
				return
			}
			if lineEnders.Contains(string(commit.Comment[firsteol-1])) {
				commit.Comment = commit.Comment[:firsteol] +
					"\n" +
					commit.Comment[firsteol:]
			}
		} else if tag, ok := event.(*Tag); ok {
			tag.Comment = strings.TrimSpace(tag.Comment) + "\n"
			if strings.Count(tag.Comment, "\n") < 2 {
				return
			}
			firsteol := strings.Index(tag.Comment, "\n")
			if tag.Comment[firsteol+1] == byte('\n') {
				return
			}
			if lineEnders.Contains(string(tag.Comment[firsteol-1])) {
				tag.Comment = tag.Comment[:firsteol] +
					"\n" +
					tag.Comment[firsteol:]
			}
		}
		control.baton.percentProgress(uint64(idx))
	})
	control.baton.endProgress()
	return false
}

//
// Examining tree states
//

// HelpCheckout says "Shut up, golint!"
func (rs *Reposurgeon) HelpCheckout() {
	rs.helpOutput(`
{SELECTION} checkout

Check out files for a specified commit into a directory.  The selection
set must resolve to a singleton commit.
`)
}

// DoCheckout checks out files for a specified commit into a directory.
func (rs *Reposurgeon) DoCheckout(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	if line == "" {
		croak("no target directory specified.")
	} else if len(selection) == 1 {
		event := repo.events[selection[0]]
		if commit, ok := event.(*Commit); ok {
			commit.checkout(line)
		} else {
			croak("not a commit.")
		}
	} else {
		croak("a singleton selection set is required.")
	}
	return false
}

// HelpDiff says "Shut up, golint!"
func (rs *Reposurgeon) HelpDiff() {
	rs.helpOutput(`
{SELECTION} diff

Display the difference between commits. Takes a selection-set argument which
must resolve to exactly two commits. Supports > redirection.
`)
}

// DoDiff displays a diff between versions.
func (rs *Reposurgeon) DoDiff(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	if len(rs.selection) != 2 {
		if logEnable(logWARN) {
			logit("a pair of commits is required.")
		}
		return false
	}
	lower, ok1 := repo.events[rs.selection[0]].(*Commit)
	upper, ok2 := repo.events[rs.selection[1]].(*Commit)
	if !ok1 || !ok2 {
		if logEnable(logWARN) {
			logit("a pair of commits is required.")
		}
		return false
	}
	dir1 := newOrderedStringSet()
	lower.manifest().iter(func(name string, _ interface{}) {
		dir1.Add(name)
	})
	dir2 := newOrderedStringSet()
	upper.manifest().iter(func(name string, _ interface{}) {
		dir2.Add(name)
	})
	allpaths := dir1.Union(dir2)
	sort.Strings(allpaths)
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	for _, path := range allpaths {
		if dir1.Contains(path) && dir2.Contains(path) {
			fromtext, _ := lower.blobByName(path)
			totext, _ := upper.blobByName(path)
			// Don't list identical files
			if !bytes.Equal(fromtext, totext) {
				lines0 := difflib.SplitLines(string(fromtext))
				lines1 := difflib.SplitLines(string(totext))
				file0 := path + " (" + lower.mark + ")"
				file1 := path + " (" + upper.mark + ")"
				diff := difflib.UnifiedDiff{
					A:        lines0,
					B:        lines1,
					FromFile: file0,
					ToFile:   file1,
					Context:  3,
				}
				text, _ := difflib.GetUnifiedDiffString(diff)
				fmt.Fprint(parse.stdout, text)
			}
		} else if dir1.Contains(path) {
			fmt.Fprintf(parse.stdout, "%s: removed\n", path)
		} else if dir2.Contains(path) {
			fmt.Fprintf(parse.stdout, "%s: added\n", path)
		} else {
			if logEnable(logWARN) {
				logit("internal error - missing path in diff")
			}
			return false
		}
	}
	return false
}

//
// Setting paths to branchify
//

// HelpBranchify says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranchify() {
	rs.helpOutput(`
branchify [DIRECTORY...]

Specify the list of directories to be treated as potential branches (to
become tags if there are no modifications after the creation copies)
when analyzing a Subversion repo. This list is ignored when reading
with the --nobranch option.  It defaults to the 'standard layout'
set of directories, plus any unrecognized directories in the
repository root.

With no arguments, displays the current branchification set.

An asterisk at the end of a path in the set means 'all immediate
subdirectories of this path, unless they are part of another (longer)
path in the branchify set'.

Note that the branchify set is a property of the reposurgeon interpreter, not
of any individual repository, and will persist across Subversion
dumpfile reads. This may lead to unexpected results if you forget
to re-set it.
`)
}

// DoBranchify is the command handler for the "brancify" command.
func (rs *Reposurgeon) DoBranchify(line string) bool {
	if rs.selection != nil {
		croak("branchify does not take a selection set")
		return false
	}
	if strings.TrimSpace(line) != "" {
		fields, err := shlex.Split(line, true)
		if err != nil {
			croak("malformed branchify command")
			return false
		}
		control.listOptions["svn_branchify"] = fields
	}
	respond("branchify " + strings.Join(control.listOptions["svn_branchify"], " "))
	return false
}

//
// Setting branch name rewriting
//

// HelpBranchmap says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranchmap() {
	rs.helpOutput(`
branchmap /REGEX/BRANCH/...

Specify the list of regular expressions used for mapping the SVN branches that
are detected by branchify. If none of the expressions match, the default behavior
applies. This maps a branch to the name of the last directory, except for trunk
and '*' which are mapped to master and root.

With no arguments the current regex replacement pairs are shown. Passing 'reset'
will clear the mapping.

String quotes and backslash escapes are *not* interpreted when parsing
the command line, this would clash with the use of backslashes as
substitution-part references. If you need to include a non-printing
character in a regexp, use its C-style escape, e.g. \s for space.

Will match each branch name against regex1 and if it matches rewrite
its branch name to branch1. If not it will try regex2 and so forth
until it either found a matching regex or there are no regexs
left. The branch name can use backreferences.

Note that the regular expressions are appended to 'refs/' without either the
needed 'heads/' or 'tags/'. This allows for choosing the right kind of branch
type.

While the syntax template above uses slashes, any first character will
be used as a delimiter (and you will need to use a different one in the
common case that the paths contain slashes).

You must give this command *before* the Subversion repository read it
is supposed to affect! It does not affect any other repository type.

Note that the branchmap set is a property of the reposurgeon interpreter,
not of any individual repository, and will persist across Subversion
dumpfile reads. This may lead to unexpected results if you forget
to re-set it.
`)
}

// DoBranchmap is the command handler for the "branchmap" command.
func (rs *Reposurgeon) DoBranchmap(line string) bool {
	if rs.selection != nil {
		croak("branchmap does not take a selection set")
		return false
	}

	line = strings.TrimSpace(line)
	if line == "reset" {
		control.branchMappings = nil
	} else if line != "" {
		control.branchMappings = make([]branchMapping, 0)
		for _, regex := range strings.Fields(line) {
			separator := regex[0]
			if separator != regex[len(regex)-1] {
				croak("Regex '%s' did not end with separator character", regex)
				return false
			}
			stuff := strings.SplitN(regex[1:len(regex)-1], string(separator), 2)
			match, replace := stuff[0], stuff[1]
			if replace == "" || match == "" {
				croak("Regex '%s' has an empty search or replace part", regex)
				return false
			}
			re, err := regexp.Compile(match)
			if err != nil {
				croak("Regex '%s' is ill-formed", regex)
				return false
			}
			control.branchMappings = append(control.branchMappings, branchMapping{re, replace})
		}
	}
	if len(control.branchMappings) != 0 {
		respond("branchmap, regex -> branch name:")
		for _, pair := range control.branchMappings {
			respond("\t" + pair.match.String() + " -> " + pair.replace)
		}
	} else {
		croak("branchmap is empty.")
	}
	return false
}

//
// Setting options
//

// HelpOptions says "Shut up, golint!"
func (rs *Reposurgeon) HelpOptions() {
	for _, opt := range optionFlags {
		fmt.Fprintf(control.baton, "%s:\n%s\n", opt[0], opt[1])
	}
}

// HelpSet says "Shut up, golint!"
func (rs *Reposurgeon) HelpSet() {
	rs.helpOutput(`
set [OPTION]

Set a (tab-completed) boolean option to control reposurgeon's
behavior.  With no arguments, displays the state of all flags and
options. The following flags and options are defined:

`)
	for _, opt := range optionFlags {
		fmt.Fprintf(control.baton, "%s:\n%s\n", opt[0], opt[1])
	}
}

// CompleteSet is a completion hook across the set of flag options that are not set.
func (rs *Reposurgeon) CompleteSet(text string) []string {
	out := make([]string, 0)
	for _, x := range optionFlags {
		if strings.HasPrefix(x[0], text) && !control.flagOptions[x[0]] {
			out = append(out, x[0])
		}
	}
	sort.Strings(out)
	return out
}

func performOptionSideEffect(opt string, val bool) {
	if opt == "progress" {
		control.baton.setInteractivity(val)
	}
	if opt == "crlf" {
		control.lineSep = "\r\n"
	}
}

func tweakFlagOptions(line string, val bool) {
	if strings.TrimSpace(line) == "" {
		for _, opt := range optionFlags {
			fmt.Printf("\t%s = %v\n", opt[0], control.flagOptions[opt[0]])
		}
	} else {
		line = strings.Replace(line, ",", " ", -1)
		for _, name := range strings.Fields(line) {
			for _, opt := range optionFlags {
				if name == opt[0] {
					control.flagOptions[opt[0]] = val
					performOptionSideEffect(opt[0], val)
					goto good
				}
			}
			croak("no such option flag as '%s'", name)
		good:
		}
	}
}

// DoSet is the handler for the "set" command.
func (rs *Reposurgeon) DoSet(line string) bool {
	tweakFlagOptions(line, true)
	return false
}

// HelpClear says "Shut up, golint!"
func (rs *Reposurgeon) HelpClear() {
	rs.helpOutput(`
clear [OPTION]

Clear a (tab-completed) boolean option to control reposurgeon's
behavior.  With no arguments, displays the state of all flags. The
following flags and options are defined:

`)
	for _, opt := range optionFlags {
		fmt.Fprintf(control.baton, "%s:\n%s\n", opt[0], opt[1])
	}
}

// CompleteClear is a completion hook across flag opsions that are set
func (rs *Reposurgeon) CompleteClear(text string) []string {
	out := make([]string, 0)
	for _, x := range optionFlags {
		if strings.HasPrefix(x[0], text) && control.flagOptions[x[0]] {
			out = append(out, x[0])
		}
	}
	sort.Strings(out)
	return out
}

// DoClear is the handler for the "clear" command.
func (rs *Reposurgeon) DoClear(line string) bool {
	tweakFlagOptions(line, false)
	return false
}

// HelpReadLimit says "Shut up, golint!"
func (rs *Reposurgeon) HelpReadLimit() {
	rs.helpOutput(`
realimit {N}

Set a maximum number of commits to read from a stream.  If the limit
is reached before EOF it will be logged. Mainly useful for benchmarking.
Without arguments, report the read limit; 0 means there is none.
`)
}

// DoReadlimit is the command handler for the "readlimit" command.
func (rs *Reposurgeon) DoReadlimit(line string) bool {
	if line == "" {
		respond("readlimit %d\n", control.readLimit)
		return false
	}
	lim, err := strconv.ParseUint(line, 10, 64)
	if err != nil {
		if logEnable(logWARN) {
			logit("ill-formed readlimit argument %q: %v.", line, err)
		}
	}
	control.readLimit = lim
	return false
}

//
// Macros and custom extensions
//

// HelpDefine says "Shut up, golint!"
func (rs *Reposurgeon) HelpDefine() {
	rs.helpOutput(`
define [NAME [TEXT]]

Define a macro.  The first whitespace-separated token is the name; the
remainder of the line is the body, unless it is '{', which begins a
multi-line macro terminated by a line beginning with '}'.

A later 'do' call can invoke this macro.

'define' by itself without a name or body produces a macro list.
`)
}

// DoDefine defines a macro
func (rs *Reposurgeon) DoDefine(lineIn string) bool {
	words := strings.SplitN(lineIn, " ", 2)
	name := words[0]
	if len(words) > 1 {
		body := words[1]
		if body[0] == '{' {
			subbody := make([]string, 0)
			depth := 0
			existingPrompt := rs.cmd.GetPrompt()
			if rs.inputIsStdin {
				rs.cmd.SetPrompt("> ")
			} else {
				rs.cmd.SetPrompt("")
			}
			defer rs.cmd.SetPrompt(existingPrompt)
			for true {
				line, err := rs.cmd.Readline()
				line = strings.TrimSpace(line)
				if err == io.EOF {
					line = "EOF"
				} else if err != nil {
					break
				}
				if depth == 0 && (line[0] == '}' || line == "EOF") {
					// done, exit loop
					break
				} else if strings.HasPrefix(line, "define") &&
					strings.HasSuffix(line, "{") {
					depth++
				} else if line[0] == '}' || line == "EOF" {
					if depth > 0 {
						depth--
					}
				}
				subbody = append(subbody, line)
			}
			rs.definitions[name] = subbody
		} else {
			rs.definitions[name] = []string{body}
		}
	} else {
		for name, body := range rs.definitions {
			if len(body) == 1 {
				respond("define %s %s\n", name, body[0])
			} else {
				respond("define %s {\n", name)
				for _, line := range body {
					respond("\t%s", line)
				}
				respond("}")
			}
		}
	}
	return false
}

// HelpDo says "Shut up, golint!"
func (rs *Reposurgeon) HelpDo() {
	rs.helpOutput(`
do {MACRO-NAME} [ARG...]

Expand and perform a macro.  The first whitespace-separated token is
the name of the macro to be called; remaining tokens replace {0},
{1}... in the macro definition. Tokens may contain whitespace if they
are string-quoted; string quotes are stripped. Macros can call macros.
If the macro expansion does not itself begin with a selection set,
whatever set was specified before the 'do' keyword is available to
the command generated by the expansion.
`)
}

// DoDo performs a macro
func (rs *Reposurgeon) DoDo(ctx context.Context, line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	words, err := shlex.Split(parse.line, true)
	if len(words) == 0 {
		croak("no macro name was given.")
		return false
	}
	if err != nil {
		croak("macro parse failed, %s", err)
		return false
	}
	name := words[0]
	macro, present := rs.definitions[name]
	if !present {
		croak("'%s' is not a defined macro", name)
		return false
	}
	args := words[1:]
	replacements := make([]string, 2*len(args))
	for i, arg := range args {
		replacements = append(replacements, fmt.Sprintf("{%d}", i), arg)
	}
	body := strings.NewReplacer(replacements...).Replace(strings.Join(macro, "\n"))
	doSelection := rs.selection

	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		defline := scanner.Text()
		// If a leading portion of the expansion body is a selection
		// expression, use it.  Otherwise we'll restore whatever
		// selection set came before the do keyword.
		expansion := rs.cmd.PreCmd(ctx, defline)
		if rs.selection == nil {
			rs.selection = doSelection
		}
		// Call the base method so RecoverableExceptions
		// won't be caught; we want them to abort macros.
		rs.cmd.OneCmd(ctx, expansion)
	}

	return false
}

// HelpUndefine says "Shut up, golint!"
func (rs *Reposurgeon) HelpUndefine() {
	rs.helpOutput(`
undef {MACRO-NAME}

Undefine the macro named in this command's first argument.
`)
}

// CompleteUndefine is a completion hook across the set of definition.
func (rs *Reposurgeon) CompleteUndefine(text string) []string {
	repo := rs.chosen()
	out := make([]string, 0)
	if repo != nil {
		for key := range rs.definitions {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// DoUndefine is the handler for the "undefine" command.
func (rs *Reposurgeon) DoUndefine(line string) bool {
	words := strings.SplitN(line, " ", 2)
	name := words[0]
	if name == "" {
		croak("no macro name was given.")
		return false
	}
	_, present := rs.definitions[name]
	if !present {
		croak("'%s' is not a defined macro", name)
		return false
	}
	delete(rs.definitions, name)
	return false
}

//
// Timequakes and bumping
//

// HelpTimequake says "Shut up, golint!"
func (rs *Reposurgeon) HelpTimequake() {
	rs.helpOutput(`
[SELECTION] timequake

Attempt to hack committer and author time stamps to make all action
stamps in the selection set (defaulting to all commits in the
repository) to be unique.  Works by identifying collisions between parent
and child, than incrementing child timestamps so they no longer
coincide. Won't touch commits with multiple parents.

Because commits are checked in ascending order, this logic will
normally do the right thing on chains of three or more commits with
identical timestamps.

Any collisions left after this operation are probably cross-branch and have
to be individually dealt with using 'timeoffset' commands.

The normal use case for this command is early in converting CVS or Subversion
repositories, to ensure that the surgical language can count on having a unique
action-stamp ID for each commit.
`)
}

// DoTimequake is the handler for the "timequake" command.
func (rs *Reposurgeon) DoTimequake(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	baton := control.baton
	//baton.startProcess("reposurgeon: disambiguating", "")
	modified := 0
	for _, event := range repo.commits(selection) {
		parents := event.parents()
		if len(parents) == 1 {
			if parent, ok := parents[0].(*Commit); ok {
				if event.committer.date.timestamp.Equal(parent.committer.date.timestamp) {
					event.bump(1)
					modified++
				}
			}
		}
		baton.twirl()
	}
	//baton.endProcess()
	respond("%d events modified", modified)
	repo.invalidateNamecache()
	return false
}

//
// Changelog processing
//

// HelpChangelogs says "Shut up, golint!"
func (rs *Reposurgeon) HelpChangelogs() {
	rs.helpOutput(`
[SELECTION] changelogs

Mine ChangeLog files for authorship data.

Takes a selection set.  If no set is specified, process all changelogs.
An optional following argument is a delimited regular expression to
match the basename of files that should be treated as changelogs; the
default is "/^ChangeLog$/".

This command assumes that changelogs are in the format used by FSF projects:
entry header lines begin with YYYY-MM-DD and are followed by a
fullname/address.

When a ChangeLog file modification is found in a clique, the entry
header at or before the section changed since its last revision is
parsed and the address is inserted as the commit author.  This is
useful in converting CVS and Subversion repositories that don't have
any notion of author separate from committer but which use the FSF
ChangeLog convention.

If the entry header contains an email address but no name, a name
will be filled in if possible by looking for the address in author
map entries.

In accordance with FSF policy for ChangeLogs, any date in an
attribution header is discarded and the committer date is used.
However, if the name is an author-map alias with an associated timezone,
that zone is used.
`)
}

var addressRE = regexp.MustCompile(`([^<@>]+\S)\s+<([^<@>\s]+@[^<@>\s]+)>`)
var wsRE = regexp.MustCompile(`\s+`)

// stringCopy forces crearion of a copy of the input strimg.  This is
// useful because the Go runtime tries not to do more allcations tn
// necessary, making string-valued references instead. Thus,
// sectioning a small string out of a very large one may cause
// the large string to be held in memory even thouggh the rest of the
// contnt is no longer referenced.
func stringCopy(a string) string {
	return (a + " ")[:len(a)]
}

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

// DoChangelogs mines repository changelogs for authorship data.
func (rs *Reposurgeon) DoChangelogs(line string) bool {
	if rs.chosen() == nil {
		croak("no repo has been chosen.")
		return false
	}
	repo := rs.chosen()
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	ok, cm, cc, cd, cl := repo.processChangelogs(selection, line, control.baton)
	if ok {
		respond("fills %d of %d authorships, changing %d, from %d ChangeLogs.", cm, cc, cd, cl)
	}
	return false
}

//
// Tarball incorporation
//
func extractTar(dst string, r io.Reader) ([]tar.Header, error) {
	files := make([]tar.Header, 0)
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return files, nil
		} else if err != nil {
			return nil, err
		} else if header == nil {
			continue
		}

		target := filepath.Join(dst, header.Name)
		if header.Typeflag == tar.TypeDir {
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return nil, err
				}
			}
		} else if header.Typeflag == tar.TypeReg {
			files = append(files, *header)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return nil, err
			}
			defer f.Close()
			if _, err := io.Copy(f, tr); err != nil {
				return nil, err
			}
		}
	}
}

// HelpIncorporate says "Shut up, golint!"
func (rs *Reposurgeon) HelpIncorporate() {
	rs.helpOutput(`
{SELECTION} incorporate [--date|--after|--firewall] [TARBALL...]

Insert the contents of specified tarballs as commit.  The tarball
names are given as arguments; if no arguments, a list is read from
stdin.  Tarballs may be gzipped or bzipped.  The initial segment of
each path is assumed to be a version directory and stripped off.  The
number of segments stripped off can be set with the option
--strip=<n>, n defaulting to 1.

Takes a singleton selection set.  Normally inserts before that commit; with
the option --after, insert after it.  The default selection set is the very
first commit of the repository.

The option --date can be used to set the commit date. It takes an argument,
which is expected to be an RFC3339 timestamp.

The generated commits have a committer field (the invoking user) and
each gets as date the modification time of the newest file in
the tarball (not the mod time of the tarball itself). No author field
is generated.  A comment recording the tarball name is generated.

Note that the import stream generated by this command is - while correct -
not optimal, and may in particular contain duplicate blobs.

With the --firewall option, generate an additional commit after the
sequence consisting only of deletes crafted to prevent the incorporarted
content fromm leaking forward.
`)
}

// DoIncorporate creates a new commit from a tarball.
func (rs *Reposurgeon) DoIncorporate(line string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	if rs.selection == nil {
		rs.selection = []int{repo.markToIndex(repo.earliestCommit().mark)}
	}
	var commit *Commit
	if len(rs.selection) == 1 {
		event := repo.events[rs.selection[0]]
		var ok bool
		if commit, ok = event.(*Commit); !ok {
			croak("selection is not a commit.")
			return false
		}
	} else {
		croak("a singleton selection set is required.")
		return false
	}
	parse := rs.newLineParse(line, orderedStringSet{"stdin"})
	defer parse.Closem()

	stripstr, present := parse.OptVal("--strip")
	var strip int
	if !present {
		strip = 1
	} else {
		var err error
		strip, err = strconv.Atoi(stripstr)
		if err != nil {
			croak("strip option must be an integer")
			return false
		}
	}

	// Tarballs are any arguments on the line, plus any on redirected stdin.
	tarballs := strings.Fields(parse.line)
	if parse.redirected {
		scanner := bufio.NewScanner(parse.stdin)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			tarballs = append(tarballs, line)
		}
	}
	if len(tarballs) == 0 {
		croak("no tarball specified.")
		return false
	}
	// The extra three slots are for the previous commit,
	// the firewall commit (if any) and the following commit.
	// The slots representing leaduing and following commits
	// could be nil if the insertion is at beginning or end of repo.
	var fw int
	if parse.options.Contains("--firewall") {
		fw = 1
	}
	segment := make([]*Commit, len(tarballs)+2+fw)

	// Compute the point where we want to start inserting generated commits
	var insertionPoint int
	if _, t := parse.OptVal("--after"); t {
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
			return false
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
		date, present := parse.OptVal("--date")
		if present {
			blank.committer.date, err = newDate(date)
			if err != nil {
				croak("invalid date: %s", date)
				return false
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

	return false
}

//
// Version binding
//

// HelpVersion says "Shut up, golint!"
func (rs *Reposurgeon) HelpVersion() {
	rs.helpOutput(`
version [EXPECT]

With no argument, display the reposurgeon version and supported VCSes.
With argument, declare the major version (single digit) or full
version (major.minor) under which the enclosing script was developed.
The program will error out if the major version has changed (which
means the surgical language is not backwards compatible).
`)
}

// DoVersion is the handler for the "version" command.
func (rs *Reposurgeon) DoVersion(line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	if line == "" {
		supported := make([]string, 0)
		for _, v := range vcstypes {
			supported = append(supported, v.name)
		}
		for _, x := range importers {
			if x.visible {
				supported = append(supported, x.name)
			}
		}
		parse.respond("reposurgeon " + version + " supporting " + strings.Join(supported, " "))
	} else {
		vmajor, _ := splitRuneFirst(version, '.')
		var major string
		if strings.Contains(line, ".") {
			fields := strings.Split(strings.TrimSpace(line), ".")
			if len(fields) != 2 {
				croak("invalid version.")
				return false
			}
			major = fields[0]
		} else {
			major = strings.TrimSpace(line)
		}
		if major != vmajor {
			croak("major version mismatch, aborting.")
			return true
		} else if control.isInteractive() {
			parse.respond("version check passed.")

		}
	}
	return false
}

//
// Exiting (in case EOT has been rebound)
//

// HelpElapsed says "Shut up, golint!"
func (rs *Reposurgeon) HelpElapsed() {
	rs.helpOutput(`
elapsed

Display elapsed time since start. Accepts output redirection.
`)
}

// DoElapsed is the handler for the "elapsed" command.
func (rs *Reposurgeon) DoElapsed(line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	parse.respond("elapsed time %v.", time.Now().Sub(rs.startTime))
	return false
}

// HelpExit says "Shut up, golint!"
func (rs *Reposurgeon) HelpExit() {
	rs.helpOutput(`
exit [>OUTFILE]

Exit cleanly, emitting a goodbye message. Accepts output redirection.

Typing EOT (usually Ctrl-D) will exit quietly.
`)
}

// DoExit is the handler for the "exit" command.
func (rs *Reposurgeon) DoExit(line string) bool {
	parse := rs.newLineParse(line, orderedStringSet{"stdout"})
	defer parse.Closem()
	parse.respond("exiting, elapsed time %v.", time.Now().Sub(rs.startTime))
	return true
}

//
// On-line help and instrumentation
//

// HelpHelp says "Shut up, golint!"
func (rs *Reposurgeon) HelpHelp() {
	rs.helpOutput(`
help [COMMAND]

Show help for a command. Follow with space and the command name.
`)
}

// HelpSelection says "Shut up, golint!"
func (rs *Reposurgeon) HelpSelection() {
	rs.helpOutput(`
A quick example-centered reference for selection-set syntax.

First, these ways of constructing singleton sets:

123        event numbered 123 (1-origin)
:345       event with mark 345
<456>      commit with legacy-ID 456 (probably a Subversion revsion)
<foo>      the tag named 'foo', or failing that the tip commit of branch foo

You can select commits and tags by date, or by date and committer:

<2011-05-25>                  all commits and tags with this date
<2011-05-25!esr>              all with this date and committer
<2011-05-25T07:30:37Z>        all commits and tags with this date and time
<2011-05-25T07:30:37Z!esr>    all with this date and time and committer
<2011-05-25T07:30:37Z!esr#2>  event #2 (1-origin) in the above set

More ways to construct event sets:

/foo/      all commits and tags containing the string 'foo' in text or metadata
           suffix letters: a=author, b=branch, c=comment in commit or tag,
                           C=committer, r=committish, p=text, t=tagger, n=name,
                           B=blob content in blobs.
           A 'b' search also finds blobs and tags attached to commits on
           matching branches.
[foo]      all commits and blobs touching the file named 'foo'.
[/bar/]    all commits and blobs touching a file matching the regexp 'bar'.
           Suffix flags: a=all fileops must match other selectors, not just
           any one; c=match against checkout paths, DMRCN=match only against
           given fileop types (no-op when used with 'c').
=C         all commits
=H         all head (branch tip) commits
=T         all tags
=B         all blobs
=R         all resets
=P         all passthroughs
=O         all orphan (parentless) commits
=U         all commits with callouts as parents
=Z         all commits with no fileops
=M         all merge commits
=F         all fork (multiple-child) commits
=L         all commits with unclean multi-line comments
=I         all commits not decodable to UTF-8
=D         all commits in which every fileop is a D or deleteall
=N         all commits and tags matching a cookie (legacy-ID) format.

@min()     create singleton set of the least element in the argument
@max()     create singleton set of the greatest element in the argument

Other special functions are available: do 'help functions' for more.

You can compose sets as follows:

:123,<foo>     the event marked 123 and the event referenced by 'foo'.
:123..<foo>    the range of events from mark 123 to the reference 'foo'

Selection sets are ordered: elements remain in the order they were added,
unless sorted by the ? suffix.

Sets can be composed with | (union) and & (intersection). | has lower
precedence than &, but set expressions can be grouped with (
). Postfixing a ? to a selection expression widens it to include all
immediate neighbors of the selection and sorts it; you can do this
repeatedly for effect. Do set negation with prefix ~; it has higher
precedence than & | but lower than ?.
`)
}

// HelpSyntax says "Shut up, golint!"
func (rs *Reposurgeon) HelpSyntax() {
	rs.helpOutput(`
Each command description begins with a syntax summary.  Mandatory parts are
in {}, optional in [], and ... says the element just before it may be repeated.
Parts in ALL-CAPS are expected to be filled in by the user.

Commands are distinguished by a command keyword.  Most take a selection set
immediately before it; see 'help selection' for details.  Some
commands take additional modifier arguments after the command keyword.

Most report-generation commands support output redirection. When
arguments for these are parsed, any argument beginning with '>' is
extracted and interpreted as the name of a file to which command
output should be redirected.  Any remaining arguments are available to
the command logic.

Some commands support input redirection. When arguments for these are
parsed, any argument beginning with '<' is extracted and interpreted
as the name of a file from which command output should be taken.  Any
remaining arguments are available to the command logic.
`)
}

// HelpFunctions says "Shut up, golint!"
func (rs *Reposurgeon) HelpFunctions() {
	rs.helpOutput(`
The following functions are available:

@min()  create singleton set of the least element in the argument
@max()  create singleton set of the greatest element in the argument
@amp()  nonempty selection set becomes all objects, empty set is returned
@par()  all parents of commits in the argument set
@chn()  all children of commits in the argument set
@dsc()  all commits descended from the argument set (argument set included)
@anc()  all commits whom the argument set is descended from (set included)
@pre()  events before the argument set
@suc()  events after the argument set
@srt()  sort the argument set by event number.
`)
}

// HelpLog says "Shut up, golint!"
func (rs *Reposurgeon) HelpLog() {
	rs.helpOutput(`
log [[+-]LOG-CLASS]...

Without an argument, list all log message classes, prepending a + if
that class is enabled and a - if not.

Otherwise, it expects a space-separated list of "<+ or -><log message
class>" entries, and enables (with +) or disables (with -) the
corresponding log message class. The special keyword "all" can be used
to affect all the classes at the same time.

For instance, "log -all +shout +warn" will disable all classes except
"shout" and "warn", which is the default setting. "log +all -svnparse"
would enable logging everything but messages from the svn parser.

A list of available message classes follows; most above "warn"
level or above are only of interest to developers, consult the source
code to learn more.

`)
	for _, item := range verbosityLevelList() {
		fmt.Println(item.k)
	}
	fmt.Println("")
}

type assoc struct {
	k string
	v uint
}

func verbosityLevelList() []assoc {
	items := make([]assoc, 0)
	for k, v := range logtags {
		items = append(items, assoc{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].v < items[j].v
	})
	return items
}

// DoLog is the handler for the "log" command.
func (rs *Reposurgeon) DoLog(lineIn string) bool {
	lineIn = strings.Replace(lineIn, ",", " ", -1)
	for _, tok := range strings.Fields(lineIn) {
		enable := tok[0] == '+'
		if !(enable || tok[0] == '-') {
			croak("an entry should start with a + or a -")
			goto breakout
		}
		tok = tok[1:]
		mask, ok := logtags[tok]
		if !ok {
			if tok == "all" {
				mask = ^uint(0)
			} else {
				croak("no such log class as %s", tok)
				goto breakout
			}
		}
		if enable {
			control.logmask |= mask
		} else {
			control.logmask &= ^mask
		}
	}
breakout:
	if len(lineIn) == 0 || control.isInteractive() {
		// We make the capabilities display in ascending value order
		out := "log"
		for i, item := range verbosityLevelList() {
			if logEnable(item.v) {
				out += " +"
			} else {
				out += " -"
			}
			out += item.k
			if (i+1)%4 == 0 {
				out += "\n\t\t"
			}
		}
		respond(out)
	}
	return false
}

// HelpLogfile says "Shut up, golint!"
func (rs *Reposurgeon) HelpLogfile() {
	rs.helpOutput(`
logfile [PATH]

Set the name of the file to which output will be redirected.
Without an argument, this command reports what logfile is set.
`)
}

// DoLogfile is the handler for the "logfile" command.
func (rs *Reposurgeon) DoLogfile(lineIn string) bool {
	if len(lineIn) != 0 {
		fp, err := os.OpenFile(lineIn, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, userReadWriteMode)
		if err != nil {
			respond("log file open failed: %v", err)
		} else {
			var i interface{} = fp
			control.logfp = i.(io.Writer)
		}
	}
	if len(lineIn) == 0 || control.isInteractive() {
		switch v := control.logfp.(type) {
		case *os.File:
			respond("logfile %s", v.Name())
		case *Baton:
			respond("logfile stdout")
		}
	}
	return false
}

// HelpPrint says "Shut up, golint!"
func (rs *Reposurgeon) HelpPrint() {
	rs.helpOutput(`
print [TEXT...] [>OUTFILE]

Ship a literal string to the terminal. All text on the command line,
including whitespace, is sent.  Intended for scripting regression
tests.  Output redirection is supported.
`)
}

// DoPrint is the handler for the "print" command.
func (rs *Reposurgeon) DoPrint(lineIn string) bool {
	parse := rs.newLineParse(lineIn, []string{"stdout"})
	defer parse.Closem()
	fmt.Fprintf(parse.stdout, "%s\n", parse.line)
	return false
}

// HelpScript says "Shut up, golint!"
func (rs *Reposurgeon) HelpScript() {
	rs.helpOutput(`
script {PATH} [ARG...]

Read and execute commands from a named file.

Takes a filename and optional following arguments.
Reads each line from the file and executes it as a command.
Text after # is ignored until end of line. The magic cookie
$0 is expanded toi the script name; $1...$b expand to the
positional arguments.  Scripts can call scripts.  A shell-
like here-document syntax led with << is interpreted.
`)
}

// DoScript is the handler for the "script" command.
func (rs *Reposurgeon) DoScript(ctx context.Context, lineIn string) bool {
	interpreter := rs.cmd
	if len(lineIn) == 0 {
		respond("script requires a file argument\n")
		return false
	}
	words := strings.Split(lineIn, " ")
	rs.callstack = append(rs.callstack, words)
	fname := words[0]
	scriptfp, err := os.Open(fname)
	if err != nil {
		croak("script failure on '%s': %s", fname, err)
		return false
	}
	defer scriptfp.Close()
	script := bufio.NewReader(scriptfp)

	existingInputIsStdin := rs.inputIsStdin
	rs.inputIsStdin = false

	interpreter.PreLoop(ctx)
	lineno := 0
	for {
		scriptline, err := script.ReadString('\n')
		lineno++
		if err == io.EOF && scriptline == "" {
			break
		}
		// Handle multiline commands
		for strings.HasSuffix(scriptline, "\\\n") {
			nexterline, err := script.ReadString('\n')
			if err == io.EOF && nexterline == "" {
				break
			}
			lineno++
			scriptline = scriptline[:len(scriptline)-2] + nexterline
		}

		scriptline = strings.TrimSpace(scriptline)

		// Simulate shell here-document processing
		if strings.Contains(scriptline, "<<") {
			heredoc, err := ioutil.TempFile("", "reposurgeon-")
			if err != nil {
				croak("script failure on '%s': %s", fname, err)
				return false
			}
			defer os.Remove(heredoc.Name())

			stuff := strings.Split(scriptline, "<<")
			scriptline = stuff[0]
			terminator := stuff[1] + "\n"
			for true {
				nextline, err := script.ReadString('\n')
				if err == io.EOF && nextline == "" {
					break
				} else if nextline == terminator {
					break
				} else {
					_, err := fmt.Fprint(heredoc, nextline)
					if err != nil {
						croak("script failure on '%s': %s", fname, err)
						return false
					}
				}
				lineno++
			}

			heredoc.Close()
			// Note: the command must accept < redirection!
			scriptline += "<" + heredoc.Name()
		}
		// End of heredoc simulation

		// Positional variables
		for i, v := range rs.callstack[len(rs.callstack)-1] {
			ref := "$" + strconv.FormatInt(int64(i), 10)
			scriptline = strings.Replace(scriptline, ref, v, -1)
		}
		scriptline = strings.Replace(scriptline, "$$", strconv.FormatInt(int64(os.Getpid()), 10), -1)

		// if the script wants to define a macro, the input
		// for the macro has to come from the script file
		existingStdin := rs.cmd.GetStdin()
		if strings.HasPrefix(scriptline, "define") && strings.HasSuffix(scriptline, "{") {
			rs.cmd.SetStdin(ioutil.NopCloser(script))
		}

		// finally we execute the command, plus the before/after steps
		originalline := scriptline
		scriptline = interpreter.PreCmd(ctx, scriptline)
		stop := interpreter.OneCmd(ctx, scriptline)
		stop = interpreter.PostCmd(ctx, stop, scriptline)

		// and then we have to put the stdin back where it
		// was, in case we changed it
		rs.cmd.SetStdin(existingStdin)

		// Abort flag is set by croak() and signals.
		// When it is set, we abort out of every nested
		// script call.
		if control.getAbort() {
			if originalline != "" && !strings.Contains(originalline, "</tmp") {
				if logEnable(logSHOUT) {
					logit("script abort on line %d %q", lineno, originalline)
				}
			} else {
				if logEnable(logSHOUT) {
					logit("script abort on line %d", lineno)
				}
			}
			break
		}
		if stop {
			break
		}
	}
	interpreter.PostLoop(ctx)

	rs.inputIsStdin = existingInputIsStdin

	rs.callstack = rs.callstack[:len(rs.callstack)-1]
	return false
}

// HelpHash says "Shut up, golint!"
func (rs *Reposurgeon) HelpHash() {
	rs.helpOutput(`
hash [--tree] [>OUTFILE]

Report Git object hashes.  This command simulates Git hash generation.

Takes a selection set, defaulting to all.  For each eligible object in the set,
returns its index  and the same hash that Git would generate for its
representation of the object. Eligible objects are blobs and commits.

With the option --bare, omit the event number; list only the hash.

With the option --tree, generate a tree hash for the specified commit rather
than the commit hash. This option is not expected to be useful for anything
but verifying the hash code itself.

This command supports output redirection.
`)
}

// DoHash is the handler for the "hash" command.
func (rs *Reposurgeon) DoHash(lineIn string) bool {
	repo := rs.chosen()
	if repo == nil {
		croak("no repo has been chosen.")
		return false
	}
	selection := rs.selection
	if rs.selection == nil {
		selection = repo.all()
	}
	parse := rs.newLineParse(lineIn, orderedStringSet{"stdout"})
	defer parse.Closem()
	for _, eventid := range selection {
		event := repo.events[eventid]
		var hashrep string
		switch event.(type) {
		case *Blob:
			hashrep = event.(*Blob).gitHash().hexify()
		case *Commit:
			if parse.options.Contains("--tree") {
				hashrep = event.(*Commit).manifest().gitHash().hexify()
			} else {
				hashrep = event.(*Commit).gitHash().hexify()
			}
		case *Tag:
			// Not yet supported
		default:
			// Other things don't have a hash
		}
		if hashrep != "" {
			if parse.options.Contains("--bare") {
				fmt.Fprintf(parse.stdout, "%s\n", hashrep)
			} else {
				fmt.Fprintf(parse.stdout, "%d: %s\n", eventid, hashrep)
			}
		}
	}
	return false
}

// DoSizeof is for developer use when optimizing structure packing to reduce memory use
// const MaxUint = ^uint(0)
// const MinUint = 0
// const MaxInt = int(MaxUint >> 1)
// const MinInt = -MaxInt - 1
func (rs *Reposurgeon) DoSizeof(lineIn string) bool {
	const wordLengthInBytes = 8
	roundUp := func(n, m uintptr) uintptr {
		return ((n + m - 1) / m) * m
	}
	explain := func(size uintptr) string {
		out := fmt.Sprintf("%3d", size)
		if size%wordLengthInBytes > 0 {
			paddedSize := roundUp(size, wordLengthInBytes)
			out += fmt.Sprintf(" (padded to %d, step down %d)", paddedSize, size%wordLengthInBytes)
		}
		return out
	}
	// Don't use respond() here, we want to be able to do "reposurgeon sizeof"
	// and get a result.
	fmt.Fprintf(control.baton, "NodeAction:        %s\n", explain(unsafe.Sizeof(*new(NodeAction))))
	fmt.Fprintf(control.baton, "RevisionRecord:    %s\n", explain(unsafe.Sizeof(*new(RevisionRecord))))
	fmt.Fprintf(control.baton, "Commit:            %s\n", explain(unsafe.Sizeof(*new(Commit))))
	fmt.Fprintf(control.baton, "Callout:           %s\n", explain(unsafe.Sizeof(*new(Callout))))
	fmt.Fprintf(control.baton, "FileOp:            %s\n", explain(unsafe.Sizeof(*new(FileOp))))
	fmt.Fprintf(control.baton, "Blob:              %s\n", explain(unsafe.Sizeof(*new(Blob))))
	fmt.Fprintf(control.baton, "Tag:               %s\n", explain(unsafe.Sizeof(*new(Tag))))
	fmt.Fprintf(control.baton, "Reset:             %s\n", explain(unsafe.Sizeof(*new(Reset))))
	fmt.Fprintf(control.baton, "Attribution:       %s\n", explain(unsafe.Sizeof(*new(Attribution))))
	fmt.Fprintf(control.baton, "blobidx:           %3d\n", unsafe.Sizeof(blobidx(0)))
	fmt.Fprintf(control.baton, "markidx:           %3d\n", unsafe.Sizeof(markidx(0)))
	fmt.Fprintf(control.baton, "revidx:            %3d\n", unsafe.Sizeof(revidx(0)))
	fmt.Fprintf(control.baton, "nodeidx:           %3d\n", unsafe.Sizeof(nodeidx(0)))
	fmt.Fprintf(control.baton, "string:            %3d\n", unsafe.Sizeof("foo"))
	fmt.Fprintf(control.baton, "[]byte:            %3d\n", unsafe.Sizeof(make([]byte, 0)))
	fmt.Fprintf(control.baton, "pointer:           %3d\n", unsafe.Sizeof(new(Attribution)))
	fmt.Fprintf(control.baton, "int:               %3d\n", unsafe.Sizeof(0))
	fmt.Fprintf(control.baton, "map[string]string: %3d\n", unsafe.Sizeof(make(map[string]string)))
	fmt.Fprintf(control.baton, "[]string:          %3d\n", unsafe.Sizeof(make([]string, 0)))
	fmt.Fprintf(control.baton, "map[string]bool:  %3d\n", unsafe.Sizeof(make(map[string]bool)))
	seq := NewNameSequence()
	fmt.Fprintf(control.baton, "raw modulus:      %-5d\n", len(seq.color)*len(seq.item))
	fmt.Fprintf(control.baton, "modulus/phi:      %-5d\n", int((float64(len(seq.color)*len(seq.item)))/phi))
	return false
}

func main() {
	ctx := context.Background()
	// need to have at least one task for the trace viewer to show any logs/regions
	ctx, task := trace.NewTask(ctx, "awesomeTask")
	defer task.End()
	defer trace.StartRegion(ctx, "main").End()
	control.init()
	rs := newReposurgeon()
	interpreter := kommandant.NewKommandant(rs)
	interpreter.EnableReadline(terminal.IsTerminal(0))

	defer func() {
		maybePanic := recover()
		control.baton.Sync()
		saveAllProfiles()
		files, err := ioutil.ReadDir("./")
		if err == nil {
			mePrefix := filepath.FromSlash(fmt.Sprintf(".rs%d", os.Getpid()))
			for _, f := range files {
				if strings.HasPrefix(f.Name(), mePrefix) && f.IsDir() {
					os.RemoveAll(f.Name())
				}
			}
		}
		if maybePanic != nil {
			panic(maybePanic)
		}
		if control.abortScript {
			os.Exit(1)
		} else {
			os.Exit(0)
		}
	}()

	if len(os.Args[1:]) == 0 {
		os.Args = append(os.Args, "-")
	}

	r := trace.StartRegion(ctx, "process-args")
	interpreter.PreLoop(ctx)
	stop := false
	for _, arg := range os.Args[1:] {
		for _, acmd := range strings.Split(arg, ";") {
			if acmd == "-" {
				// Next two conditionals are written
				// this way so that, e,g. "set
				// interactive" before "-" can force
				// interactive mode.
				if terminal.IsTerminal(0) {
					control.flagOptions["interactive"] = true
				}
				if terminal.IsTerminal(1) {
					control.flagOptions["progress"] = true
				}
				control.baton.setInteractivity(control.flagOptions["interactive"])
				r := trace.StartRegion(ctx, "repl")
				interpreter.CmdLoop(ctx, "")
				r.End()
			} else {
				// A minor concession to people used
				// to GNU conventions.  Makes
				// "reposurgeon --help" and
				// "reposurgeon --version" work as
				// expected.
				if strings.HasPrefix(acmd, "--") {
					acmd = acmd[2:]
				}
				acmd = interpreter.PreCmd(ctx, acmd)
				stop = interpreter.OneCmd(ctx, acmd)
				stop = interpreter.PostCmd(ctx, stop, acmd)
				if stop {
					break
				}
			}
		}
	}
	interpreter.PostLoop(ctx)
	r.End()
	// Fall through to defer hook.
}

// end
