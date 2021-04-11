// Reposurgeon is an editor/converter for version-control histories.
//
// This file includes the program main and defines the syntax for the DSL.
//
// Copyright by Eric S. Raymond
// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"os/signal"
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
	terminfo "github.com/xo/terminfo"
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
	logcounter int
	signals    chan os.Signal
	logmutex   sync.Mutex
	// The abort flag
	abortScript  bool
	abortLock    sync.Mutex
	listOptions  map[string]orderedStringSet
	profileNames map[string]string
	startTime    time.Time
	baton        *Baton
	GCPercent    int
}

func (ctx *Control) isInteractive() bool {
	return ctx.flagOptions["interactive"]
}

func (ctx *Control) init() {
	ctx.flagOptions = make(map[string]bool)
	ctx.listOptions = make(map[string]orderedStringSet)
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
		}
	}()
	ctx.startTime = time.Now()
	control.lineSep = "\n"
	control.GCPercent = 100 // Golang's starting value
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
	proc         *exec.Cmd
}

// Parse precondition flags
const parseNONE = 0
const (
	parseREPO         uint = 1 << iota // Requires a loaded repo
	parseALLREPO                       // Requires a loaded repo and selection sets defaults to all
	parseNOSELECT                      // Giving a selection set is an error
	parseNEEDSELECT                    // Command requires an explicit selection set
	parseNEEDREDIRECT                  // Command requires aredirect, not a file name argument
)

func (rs *Reposurgeon) newLineParse(line string, parseflags uint, capabilities orderedStringSet) *LineParse {
	if rs.chosen() == nil && (parseflags&(parseREPO|parseALLREPO)) != 0 {
		panic(throw("command", "no repo has been chosen."))
	}
	if rs.selection == nil && (parseflags&parseALLREPO) != 0 {
		rs.selection = rs.chosen().all()
	}
	if rs.selection != nil && (parseflags&parseNOSELECT) != 0 {
		panic(throw("command", "command does not take a selection set"))
	}
	if rs.selection == nil && (parseflags&parseNEEDSELECT) != 0 {
		panic(throw("command", "command requires an explicit selection"))
	}

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
	// The reason for the < in the regexp is to prevent false matches
	// on legacy IDs in singleton-selection literals.
	match := regexp.MustCompile("<[^> ]+ ").FindStringIndex(lp.line + " ")
	if match != nil {
		if !caps["stdin"] {
			panic(throw("command", "no support for < redirection"))
		}
		lp.infile = lp.line[match[0]+1 : match[1]-1]
		if lp.infile != "" && lp.infile != "-" {
			lp.stdin, err = os.Open(lp.infile)
			if err != nil {
				panic(throw("command", "can't open %s for read", lp.infile))
			}
			lp.closem = append(lp.closem, lp.stdin)
		}
		lp.line = lp.line[:match[0]] + lp.line[match[1]-1:]
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
	// This collides with regexp alternation.  We require a whitespace character
	// after the pipe bar and that it be either at BOL or have a preceding whitespace,
	// which is a partial prevention.  This code looks a little weird because the command
	// verb and following whitespace have already been popped off when lp.line gets here,
	// so the pipe bar can in fact be at index zero.
	pipeIndex := strings.Index(lp.line, "|")
	isspace := func(b byte) bool { return unicode.IsSpace(rune(b)) }
	if pipeIndex != -1 && len(lp.line) > 2 && (pipeIndex == 0 || isspace(lp.line[pipeIndex-1])) && isspace(lp.line[pipeIndex+1]) {
		if !caps["stdout"] {
			panic(throw("command", "no support for | redirection"))
		}
		cmd := strings.TrimSpace(lp.line[pipeIndex+1:])
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		lp.proc = exec.Command(shell)
		lp.proc.Args = append(lp.proc.Args, "-c")
		lp.proc.Args = append(lp.proc.Args, cmd)
		var err error
		lp.stdout, err = lp.proc.StdinPipe()
		if err != nil {
			panic(throw("command", fmt.Sprintf("can't pipe to %q, error %v", cmd, err)))
		}
		lp.proc.Stdout = control.baton
		lp.proc.Stderr = control.baton
		lp.closem = append(lp.closem, lp.stdout)
		err = lp.proc.Start()
		if err != nil {
			panic(throw("command", fmt.Sprintf("can't run %q, error %v", cmd, err)))
		}
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

	if len(lp.line) > 0 && (parseflags&parseNEEDREDIRECT) != 0 {
		panic(throw("command", "command does not take a filename argument - use redirection instead"))
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
	//if lp.proc != nil {
	//	lp.proc.Wait()
	//}
}

// respond is to be used for console messages that shouldn't be logged
func (lp *LineParse) respond(msg string, args ...interface{}) {
	content := fmt.Sprintf(msg, args...)
	control.baton.printLogString(content + control.lineSep)
}

func delimitedRegexp(in string) (out string, re bool) {
	leader := in[0]
	delimited := in[0] == in[len(in)-1] && unicode.IsPunct(rune(in[0]))
	if delimited {
		in = in[1 : len(in)-1]
	}
	isRe := delimited && leader != 39 // ASCII single quote
	return in, isRe
}

func getPattern(sourcepattern string) *regexp.Regexp {
	sourcepattern, isRe := delimitedRegexp(sourcepattern)
	if !isRe {
		sourcepattern = "^" + regexp.QuoteMeta(sourcepattern) + "$"
	}
	sourceRE, err := regexp.Compile(sourcepattern)
	if err != nil {
		panic(throw("command", err.Error()+"in command pattern argument"))
	}
	return sourceRE
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

var helpLinks = [][]string{
	{"options", "control-options"},
	{"regexp", "regular_expressions"},
}

// helpOutput handles Go multiline literals that may have a leading \n
// to make them more readable in source. It just clips off any leading \n.
func (rs *Reposurgeon) helpOutput(help string) {
	if help[0] == '\n' {
		help = help[1:]
	}
	if control.flagOptions["asciidoc"] {
		// Item format is expected to be: one line of BNF, a
		// blank separator line, and one or more
		// blank-line-separated paragraphs of running text.
		lines := strings.Split(help, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		os.Stdout.WriteString(lines[0] + "::\n")
		indent := true
		for _, line := range lines[2:] {
			if line == "" {
				os.Stdout.WriteString("+\n")
				indent = false
			} else {
				// turn "help foo" in the prose into a link in asciidoctor syntax
				for _, link := range helpLinks {
					line = strings.ReplaceAll(line, "help "+link[0], "<<"+link[1]+",help "+link[0]+">>")
				}
				if indent {
					os.Stdout.WriteString("   ")
				}
				os.Stdout.WriteString(line + "\n")
			}
		}
	} else if terminal.IsTerminal(1) && control.isInteractive() {
		pager, err := NewPager(ti)
		if err != nil {
			fmt.Fprintln(os.Stderr, fmt.Errorf("Unable to start a pager: %w", err).Error())
		} else {
			io.WriteString(pager, help)
			pager.Close()
			return
		}
	} else {
		// Dump as plain text
		os.Stdout.WriteString(help)
	}
}

// helpOutputMisc prints help messages that are not about a single command
func (rs *Reposurgeon) helpOutputMisc(help string) {
	if help[0] == '\n' {
		help = help[1:]
	}
	// Dump as plain text
	control.baton.printLogString(help)
}

func (rs *Reposurgeon) inScript() bool {
	return len(rs.callstack) > 0
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

// DoHelp takes over printing the help index from the Kommandant
func (rs *Reposurgeon) DoHelp(ctx context.Context, argIn string) (stopOut bool) {
	if argIn != "" {
		// user asked for help about a specific command, so let the Kommandant handle it
		rs.cmd.DoHelp(ctx, argIn)
	} else {
		var out io.WriteCloser
		var maxWidth = 76
		if terminal.IsTerminal(1) && control.isInteractive() {
			width, _, err := terminal.GetSize(0)
			if err == nil {
				maxWidth = width - 4
			}
			pager, err := NewPager(ti)
			if err != nil {
				fmt.Fprintln(os.Stderr, fmt.Errorf("Unable to start a pager: %w", err).Error())
				return false
			}
			out = pager
		} else {
			out = os.Stdout
		}
		longest := 43
		for _, h := range _Helps {
			hasUL := len(ti.Strings[terminfo.EnterUnderlineMode]) != 0
			lines := wrap(h.commands, maxWidth-longest)
			isdigit := func(b byte) bool { return unicode.IsDigit(rune(b)) }
			for idx, line := range lines {
				if idx == 0 {
					if hasUL && isdigit(h.title[0]) {
						ti.Fprintf(out, terminfo.EnterUnderlineMode)
						io.WriteString(out, h.title)
						ti.Fprintf(out, terminfo.ExitUnderlineMode)
						fmt.Fprintf(out, "%*s%s\n", longest-len(h.title), " ", line)
					} else {
						fmt.Fprintf(out, "%s%*s%s\n", h.title, longest-len(h.title), " ", line)
					}
				} else {
					fmt.Fprintf(out, "%*s%s\n", longest, " ", line)
				}
			}
		}
		out.Close()
	}
	return false
}

func wrap(cmds []string, maxWidth int) []string {
	out := make([]string, 0)
	var cur string
	for idx, cmd := range cmds {
		if len(cur)+len(cmd)+1 <= maxWidth {
			cur += cmd
		} else {
			out = append(out, cur)
			cur = cmd
		}
		if idx != len(cmds)-1 {
			cur += ", "
		}
	}
	out = append(out, cur)
	return out
}

//
// Helpers
//

func (rs *Reposurgeon) accumulateCommits(subarg *fastOrderedIntSet,
	operation func(*Commit) []CommitLike, recurse bool) *fastOrderedIntSet {
	return rs.chosen().accumulateCommits(subarg, operation, recurse)
}

// Generate a repository report on all events with a specified display method.
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

//
// Command implementation begins here
//

// The DSL grammar obeys some rules intended to make it easy to
// remember and to parse.  These rules are expressed in the BNF given
// in the embedded help through choice of metavariable (capitalized)
// names.
//
// Every command has a leading keyword argument (this is enforced by
// using Kommandant, which dispatches on these keywords). Some
// commands have a required second subcommand keyword.
//
// Many commands have a selection expression before the keyword.
// Selection expressions have their own lexical rules and grammar,
// not described in this comment.
//
// Lexically, almost every command line is processed as a sequence
// of tokens. There are only two exceptions to this: "shell" and "print",
// which consume the entire untokenized remainder of the input line other
// than its leading space.
//
// There are three kinds of tokens: barewords (including syntax keywords),
// strings (bounded by double quotes, may contain whitespace)
// and pattern expressions.  A pattern expression is interpreted as (1) a regexp
// if its first and last characters matchm are punctuation, and are anything other
// than an ASCII single quote, (2) a literal string if its first and last characters
// are ASCII single quotes, or (3) a literal string, otherwise. Pattern expressions
// may not contain whitespace, unlike the regular expressions in selections.
//
// If a command has a pattern-expression argument, it has exactly one
// and it is the first (it may be optional).  There is a half-exception to
// this rule: filter, with a first argument that may be either a string
// or a pattern expression
//
// All optional arguments and keywords follow any required arguments
// and keywords.  There are two exceptions to this rule, in
// the "attribution" and "remove" commands; these sometimes have
// required arguments following optionals.  Also, "tag" and "reset"
// have two different sequences not very well expressed in the BNF -
// the create case requires one string or bareword newname, while move
// and rename require two arguments.
//
// All unbounded lists of arguments are final in their
// command syntax.
//
// No command has more than three arguments (excluding syntactic
// keywords), except for those ending with string/bareword lists.
//
// All uses of alternation in the BNF are a choice of keywords, except
// in the "add", "attribution", "path", and "split" commands.

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

Typing EOT (usually Ctrl-D) is a shortcut for this.
`)
}

// DoQuit is the handler for the "quit" command.
func (rs *Reposurgeon) DoQuit(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
	return true
}

// HelpShell says "Shut up, golint!"
func (rs *Reposurgeon) HelpShell() {
	rs.helpOutput(`
shell [COMMAND-TEXT]

Run a shell command. Honors the $SHELL environment variable.

"!" is a shortcut for this command. 
`)
}

// DoShell is the handler for the "shell" command.
func (rs *Reposurgeon) DoShell(line string) bool {
	//Can't use newLineParse() here, it false-matches on shell redirect syntax
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

//
// On-line help and instrumentation
//

// HelpResolve says "Shut up, golint!"
func (rs *Reposurgeon) HelpResolve() {
	rs.helpOutput(`
SELECTION resolve

Does nothing but resolve a selection-set expression
and report the resulting event-number set to standard
output. The remainder of the line after the command,
if any, is used as a label for the output.

Implemented mainly for regression testing, but may be useful
for exploring the selection-set language.

The parenthesized literal produced by this command is valid
selection-set syntax; it can be pasted into a script for 
re-use.
`)
}

// DoResolve displays the set of event numbers generated by a selection set.
func (rs *Reposurgeon) DoResolve(line string) bool {
	if rs.selection == nil {
		respond("No selection\n")
	} else {
		out := ""
		for _, i := range rs.selection {
			out += fmt.Sprintf("%d,", i+1)
		}
		if len(out) > 0 {
			out = out[:len(out)-1]
		}
		out = "(" + out + ")"
		if line != "" {
			control.baton.printLogString(fmt.Sprintf("%s: %s\n", line, out))
		} else {
			control.baton.printLogString(fmt.Sprintf("%v\n", out))
		}
	}
	return false
}

// HelpAssign says "Shut up, golint!"
func (rs *Reposurgeon) HelpAssign() {
	rs.helpOutput(`
SELECTION assign [--singleton] [NAME]

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

Example:

----
# Assign to the name "cvsjunk" the selection set of all commits with a
# boilerplate CVS empty log message in the comment. 
/empty log message/ assign cvsjunk
----
`)
}

// DoAssign is the handler for the "assign" command,
func (rs *Reposurgeon) DoAssign(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	repo := rs.chosen()
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
unassign NAME

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
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
	repo := rs.chosen()
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
names [>OUTFILE]

List all known symbolic names of branches and tags. 
Tells you what things are legal within angle brackets and
parentheses.
`)
}

// DoNames is the handler for the "names" command,
func (rs *Reposurgeon) DoNames(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	branches := rs.chosen().branchset()
	//sortbranches.Sort()
	for _, branch := range branches {
		fmt.Fprintf(parse.stdout, "branch %s\n", branch)
	}
	for _, event := range rs.chosen().events {
		if tag, ok := event.(*Tag); ok {
			fmt.Fprintf(parse.stdout, "tag    %s\n", tag.tagname)

		}
	}
	return false
}

// HelpHistory says "Shut up, golint!"
func (rs *Reposurgeon) HelpHistory() {
	rs.helpOutput(`
history

Dump your command list from this session so far.

You can do Ctrl-P or up-arrow to scroll back through the command
history list, and Ctrl-N or down-arrow to scroll forward in it.
Tab-completion on command keywords is available in combination
with these commands.
`)
}

// DoHistory is the handler for the "history" command,
func (rs *Reposurgeon) DoHistory(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
	for _, line := range rs.history {
		control.baton.printLogString(line)
	}
	return false
}

// HelpIndex says "Shut up, golint!"
func (rs *Reposurgeon) HelpIndex() {
	rs.helpOutput(`
[SELECTION] index [>OUTFILE]

Display four columns of info on selected events: their number, their
type, the associated mark (or '-' if no mark) and a summary field
varying by type.  For a branch or tag it's the reference; for a commit
it's the commit branch; for a blob it's a space-separated list of the 
repository path of the files with the blob as content.

This command can be safely interrupted with ^C, returning you to the
prompt.
`)
}

// DoIndex generates a summary listing of events.
func (rs *Reposurgeon) DoIndex(lineIn string) bool {
	parse := rs.newLineParse(lineIn, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	// We could do all this logic using reportSelect() and index() methods
	// in the events, but that would have two disadvantages.  First, we'd
	// get a default-set computation we don't want.  Second, for this
	// function it's helpful to have the method strings close together so
	// we can maintain columnation.
	for _, eventid := range rs.selection {
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
			fmt.Fprintf(parse.stdout, "%6d tag    %6s    %4s\n", eventid+1, e.committish, e.tagname)
		case *Reset:
			committish := e.committish
			if committish == "" {
				committish = "-"
			}
			fmt.Fprintf(parse.stdout, "%6d branch %6s    %s\n", eventid+1, committish, e.ref)
		default:
			fmt.Fprintf(parse.stdout, "     ?             -    %s", e)
		}
		if control.getAbort() {
			break
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
profile [live|start|save|bench] [PORT | SUBJECT [FILENAME]]

Manages data collection for profiling.

Corresponding subcommands are these:

    profile live [PORT]

	Starts an http server on the specified port which serves
	the profiling data. If no port is specified, it defaults
	to port 1234. Use in combination with pprof:

	    go tool pprof -http=":8080" http://localhost:1234/debug/pprof/<subject>

    profile start {SUBJECT} {FILENAME}

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

    profile bench

	Report elapsed time and memory usage in the format expected by 
	repobench. Note: this comment is not intended for interactive
	use or to be used by scripts other than repobench.  The output
	format may change as repobench does.

	Runs a garbage-collect before reporting so the figure will
	better reflect storage currently held in loaded repositories;
	this will not affect the reported high-water mark.
	For a list of available profile subjects, call the profile
	command without arguments. The list is in part extracted from the
	Go runtime and is subject to change.

For documentation on the Go porofiler used by the live and start modes, see

https://github.com/google/pprof/blob/master/doc/README.md
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
		case "bench":
			rs.newLineParse(line, parseNOSELECT, nil)
			var memStats runtime.MemStats
			debug.FreeOSMemory()
			runtime.ReadMemStats(&memStats)
			const MB = 1e6
			fmt.Printf("%d %.2f %.2f %.2f\n",
				control.readLimit, time.Since(control.startTime).Seconds(),
				float64(memStats.HeapAlloc)/MB, float64(memStats.TotalAlloc)/MB)
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
`)
}

// DoTiming reports repo-analysis times
func (rs *Reposurgeon) DoTiming(line string) bool {
	parse := rs.newLineParse(line, parseREPO|parseNOSELECT, orderedStringSet{"stdout"})
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

Report memory usage.  Runs a garbage-collect before reporting so the
figure will better reflect storage currently held in loaded repositories;
this will not affect the reported high-water mark.
`)
}

// DoMemory is the handler for the "memory" command.
func (rs *Reposurgeon) DoMemory(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, orderedStringSet{"stdout"})
	defer parse.Closem()
	var memStats runtime.MemStats
	debug.FreeOSMemory()
	runtime.ReadMemStats(&memStats)
	const MB = 1e6
	fmt.Fprintf(parse.stdout, "Heap: %.2fMB  High water: %.2fMB\n",
		float64(memStats.HeapAlloc)/MB, float64(memStats.TotalAlloc)/MB)
	return false
}

//
// Information-gathering
//

// HelpStats says "Shut up, golint!"
func (rs *Reposurgeon) HelpStats() {
	rs.helpOutput(`
stats [>OUTFILE]

Report object counts for the loaded repository.
`)
}

// DoStats reports information on repositories.
func (rs *Reposurgeon) DoStats(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	var blobs, commits, tags, resets, passthroughs int
	for _, i := range rs.selection {
		event := repo.events[i]
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
	return false
}

// HelpCount says "Shut up, golint!"
func (rs *Reposurgeon) HelpCount() {
	rs.helpOutput(`
SELECTION count [>OUTFILE]

Report a count of items in the selection set. Default set is everything
in the currently-selected repo.
`)
}

// DoCount is the command handler for the "count" command.
func (rs *Reposurgeon) DoCount(lineIn string) bool {
	parse := rs.newLineParse(lineIn, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	fmt.Fprintf(parse.stdout, "%d\n", len(rs.selection))
	return false
}

// HelpList says "Shut up, golint!"
func (rs *Reposurgeon) HelpList() {
	rs.helpOutput(`
[SELECTION] list [>OUTFILE]

Display commits in a human-friendly format; the first column is raw
event numbers, the second a timestamp in UTC. If the repository
has legacy IDs, they will be displayed in the third column. The
leading portion of the comment follows.

This command can be safely interrupted with ^C, returning you to the
prompt.
`)
}

// DoList generates a human-friendly listing of events.
func (rs *Reposurgeon) DoList(lineIn string) bool {
	parse := rs.newLineParse(lineIn, parseREPO, orderedStringSet{"stdout"})
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

// HelpTags says "Shut up, golint!"
func (rs *Reposurgeon) HelpTags() {
	rs.helpOutput(`
[SELECTION] tags [>OUTFILE]

Display tags and resets: three fields, an event number and a type and a name.
Branch tip commits associated with tags are also displayed with the type
field 'commit'.

This command can be safely interrupted with ^C, returning you to the
prompt.
`)
}

// DoTags is the handler for the "tags" command.
func (rs *Reposurgeon) DoTags(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
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

This command can be safely interrupted with ^C, returning you to the
prompt.
`)
}

// DoStamp lists action stamps for each element of the selection set
func (rs *Reposurgeon) DoStamp(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
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
unwieldy.
`)
}

// DoSizes reports branch relative sizes.
func (rs *Reposurgeon) DoSizes(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	sizes := make(map[string]int)
	for _, i := range rs.selection {
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
				croak("internal error: target of tag %s is nil", tag.tagname)
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

Options to issue only partial reports are supported; "lint --options"
or "lint -?" lists them.

The options and output format of this command are unstable; they may
change without notice as more sanity checks are added.
`)
}

// DoLint looks for possible data malformations in a repo.
func (rs *Reposurgeon) DoLint(line string) (StopOut bool) {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	if parse.options.Contains("--options") || parse.options.Contains("-?") {
		fmt.Fprint(parse.stdout, `
--deletealls    -d     report mid-branch deletealls
--connected     -c     report disconnected commits
--roots         -r     report on multiple roots
--attributions  -a     report on anomalies in usernames and attributions
--uniqueness    -u     report on collisions among action stamps
--cvsignores    -i     report if .cvsignore files are present
--options       -?     list available options
`[1:])
		return false
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
	cvsignores := 0
	rs.chosen().walkEvents(rs.selection, func(idx int, event Event) {
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
		if parse.options.Contains("--deletealls") || parse.options.Contains("-d") {
			for _, op := range commit.operations() {
				if strings.HasSuffix(op.Path, ".cvsignore") {
					cvsignores++
				}
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
	if cvsignores > 0 {
		fmt.Fprintf(parse.stdout, "%d .cvsignore operations found.\n", cvsignores)
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
	rs.newLineParse(line, parseNOSELECT, nil)
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
file. The argument tab-completes using the list of supported systems.

The source type affects the interpretation of legacy IDs (for
purposes of the =N visibility set and the 'references' command) by
controlling the regular expressions used to recognize them. If no
preferred output type has been set, it may also change the output
format of stream files made from the repository.

The source type is reliably set whenever a live repository is read, or
when a Subversion stream or Fossil dump is interpreted - but not
necessarily by other stream files. Streams generated by
cvs-fast-export(1) using the "--reposurgeon" option are detected as CVS. In
some other cases, the source system is detected from the presence of
magic $-headers in contents blobs.
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
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
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
gc [GOGC] [>OUTFILE]

Trigger a garbage collection. Scavenges and removes all blob events
that no longer have references, e.g. as a result of delete operations
on repositories. This is followed by a Go-runtime garbage collection.

The optional argument, if present, is passed as a
https://golang.org/pkg/runtime/debug/#SetGCPercent[SetPercentGC]
call to the Go runtime. The initial value is 100; setting it lower
causes more frequent garbage collection and may reduces maximum
working set, while setting it higher causes less frequent garbage
collection and will raise maximum working set.

The current GC percentage (after setting it, if an argument was given)
is reported.
`)
}

// DoGc is the handler for the "gc" command.
func (rs *Reposurgeon) DoGc(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
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
		control.GCPercent = debug.SetGCPercent(v)
	}
	respond("%d", control.GCPercent)
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

With no argument, lists the names of the currently stored repositories.  
The second column is '*' for the currently selected repository, '-'
for others.

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
	rs.newLineParse(line, parseNOSELECT, nil)
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
	rs.newLineParse(line, parseNOSELECT, nil)
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
rename NEW-NAME

Rename the currently chosen repo; requires an argument.  Won't do it
if there is already one by the new name.
`)
}

// DoRename changes the name of a repository.
func (rs *Reposurgeon) DoRename(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
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
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
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
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
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
read [--OPTION...] [<INFILE | DIRECTORY]

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

Various options and special features of this command are described in
the long-form manual. See "help branchify"  for one of the more
important ones.
`)
}

// DoRead reads in a repository for surgery.
func (rs *Reposurgeon) DoRead(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, []string{"stdin"})
	// Don't defer parse.Closem() here - you'll nuke the seekstream that
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
		repo.fastImport(context.TODO(), parse.stdin, parse.options.toStringSet(), "", control.baton)
	} else if parse.line == "" || parse.line == "." {
		var err2 error
		// This is slightly asymmetrical with the write side, which
		// interprets an empty argument list as '-'
		cdir, err2 := os.Getwd()
		if err2 != nil {
			croak(err2.Error())
			return false
		}
		repo, err2 = readRepo(cdir, parse.options.toStringSet(), rs.preferred, rs.extractor, control.flagOptions["quiet"], control.baton)
		if err2 != nil {
			croak(err2.Error())
			return false
		}
	} else if isdir(parse.line) {
		var err2 error
		repo, err2 = readRepo(parse.line, parse.options.toStringSet(), rs.preferred, rs.extractor, control.flagOptions["quiet"], control.baton)
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

// HelpBranchify says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranchify() {
	rs.helpOutput(`
--branchify=DIRECTORY[:DIRECTORY]...

Specify a colon-separated list of directories to be treated as
potential branches (to become tags if there are no modifications
after the creation copies) when analyzing a Subversion repo. This
option is ignored when reading with the --nobranch option.  It
defaults to the 'standard layout' set of directories, plus any
unrecognized directories in the repository root.

An asterisk at the end of a path in the set means 'all immediate
subdirectories of this path, unless they are part of another (longer)
path in the branchify set'.

----
# This is what the branchify option would look like
# If you needed to specify the default set of branch patterns.
read --branchify=trunk:tags/*:branches/*:* <example.svn
----

`)
}

// HelpWrite says "Shut up, golint!"
func (rs *Reposurgeon) HelpWrite() {
	rs.helpOutput(`
[SELECTION] write [--legacy] [--format=fossil] [--noincremental] [--callout] [>OUTFILE|-|DIRECTORY]

Dump a fast-import stream representing selected events to standard
output (if second argument is empty or '-') or via > redirect to a file.
Alternatively, if there is no redirect and the argument names a
directory the repository is rebuilt into that directory, with any
selection set argument being ignored; if that target directory is
nonempty its contents are backed up to a save directory.

Property extensions will be omitted if the importer for the
preferred repository type cannot digest them.

Various options and special features of this command are described in
the long-form manual.
`)
}

// DoWrite streams out the results of repo surgery.
func (rs *Reposurgeon) DoWrite(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
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
		rs.chosen().fastExport(rs.selection, parse.stdout, parse.options.toStringSet(), rs.preferred, control.baton)
	} else if isdir(parse.line) {
		err := rs.chosen().rebuildRepo(parse.line, parse.options.toStringSet(), rs.preferred, control.baton)
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
func (rs *Reposurgeon) DoInspect(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
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
		fmt.Fprintln(parse.stdout, utf8trunc(header, 73))
		fmt.Fprint(parse.stdout, event.String())
	}

	return false
}

// HelpStrip says "Shut up, golint!"
func (rs *Reposurgeon) HelpStrip() {
	rs.helpOutput(`
[SELECTION] strip {--blobs|--reduce}

This is intended for producing reduced test cases from large repositories.

Replace the blobs in the selected repository with self-identifying stubs;
and/or strip out topologically uninteresting commits.  The options for
this are "--blobs" and "--reduce" respectively; the default is "--blobs".

A selection set is effective only with the "--blobs" option, defaulting to all
blobs. The "--reduce" mode always acts on the entire repository.

With the modifier "--reduce", perform a topological reduction that
throws out uninteresting commits.  If a commit has all file
modifications (no deletions or copies or renames) and has exactly one
ancestor and one descendant, then it may be boring.  To be fully
boring, it must also not be referred to by any tag or reset.
Interesting commits are not boring, or have a non-boring parent or
non-boring child.
`)
}

// CompleteStrip is a completion hook across strip's modifiers.
func (rs *Reposurgeon) CompleteStrip(text string) []string {
	return []string{"--blobs", "--reduce"}
}

// DoStrip strips out content to produce a reduced test case.
func (rs *Reposurgeon) DoStrip(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	var striptypes orderedStringSet
	var oldlen int
	if line == "" {
		striptypes = orderedStringSet{"--blobs"}
	} else {
		striptypes = newOrderedStringSet(strings.Fields(line)...)
	}
	if striptypes.Contains("--blobs") {
		for _, ei := range rs.selection {
			if blob, ok := repo.events[ei].(*Blob); ok {
				blob.setContent([]byte(fmt.Sprintf("Blob at %s\n", blob.mark)), noOffset)
			}
		}
	}
	if striptypes.Contains("--reduce") {
		oldlen = len(repo.events)
		repo.reduce()
		respond("From %d to %d events.", oldlen, len(repo.events))
	}

	if striptypes.Contains("--obscure") {
		seq := NewNameSequence()
		pathMutator := func(s string) string {
			if s == "" {
				return ""
			}
			parts := strings.Split(filepath.ToSlash(s), "/")
			for i := range parts {
				parts[i] = seq.obscureString(parts[i])
			}
			return filepath.FromSlash(strings.Join(parts, "/"))
		}
		for _, event := range repo.events {
			if commit, ok := event.(*Commit); ok {
				for i := range commit.operations() {
					commit.fileops[i].Path = pathMutator(commit.fileops[i].Path)
					commit.fileops[i].Source = pathMutator(commit.fileops[i].Source)
				}
			}
		}
	}

	return false
}

// HelpGraph says "Shut up, golint!"
func (rs *Reposurgeon) HelpGraph() {
	rs.helpOutput(`
[SELECTION] graph [>OUTFILE]

Emit a visualization of the commit graph in the DOT markup language
used by the graphviz tool suite.  This can be fed as input to the main
graphviz rendering program dot(1), which will yield a viewable
image.

Because graph supports output redirection, you can do this:

----
graph | dot -Tpng | display
----

You can substitute in your own preferred image viewer, of course.
`)
}

// Most comment characters we want to fit in a commit box
const graphCaptionLength = 32

// Some links to reopository viewers to look at for styling ideas:
//
// https://gitlab.com/techtonik/repodraw/-/blob/master/plotrepo.rb
// https://github.com/gto76/ascii-git-graph-to-png
// https://github.com/hoduche/git-graph
// https://github.com/bast/gitink/
// https://fosdem.org/2021/schedule/event/git_learning_game/

// DoGraph dumps a commit graph.
func (rs *Reposurgeon) DoGraph(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	rs.chosen().doGraph(rs.selection, parse.stdout)
	return false
}

// HelpRebuild says "Shut up, golint!"
func (rs *Reposurgeon) HelpRebuild() {
	rs.helpOutput(`
rebuild [DIRECTORY]

Rebuild a repository from the state held by reposurgeon.  This command
does not take a selection set.

The single argument, if present, specifies the target directory in
which to do the rebuild; if the repository read was from a repo
directory (and not a git-import stream), it defaults to that
directory.  If the target directory is nonempty its contents are
backed up to a save directory.  Files and directories on the
repository's preservation list are copied back from the backup directory
after repo rebuild. The default preserve list depends on the
repository type, and can be displayed with the "stats" command.

If reposurgeon has a nonempty legacy map, it will be written to a file
named "legacy-map" in the repository subdirectory as though by a
"legacy write" command. (This will normally be the case for
Subversion and CVS conversions.)
`)
}

// DoRebuild rebuilds a live repository from the edited state.
func (rs *Reposurgeon) DoRebuild(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	if len(rs.selection) != 0 {
		croak("rebuild does not take a selection set")
		return false
	}
	err := rs.chosen().rebuildRepo(parse.line, parse.options.toStringSet(), rs.preferred, control.baton)
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
[SELECTION] msgout [--filter=PATTERN] [--blobs]

Emit a file of messages in RFC822 format representing the contents of
repository metadata. Takes a selection set; members of the set other
than commits, annotated tags, and passthroughs are ignored (that is,
presently, blobs and resets).

May have an option --filter, followed by a pattern expression.  If this
is given, only headers with names matching it are emitted.  In this
control the name of the header includes its trailing colon.  The value
of the option must be a pattern expression. See "help regexp"
for information on the regexp syntax.

Blobs may be included in the output with the option --blobs.

The following example produces a mailbox of commit comments in a
decluttered form that is convenient for editing:

----
=C msgout --filter=/Event-Number:|Committer:|Author:|Check-Text:/
----

This command can be safely interrupted with ^C, returning you to the
prompt.
`)
}

// DoMsgout generates a message-box file representing event metadata.
func (rs *Reposurgeon) DoMsgout(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
	defer parse.Closem()

	var filterRegexp *regexp.Regexp
	s, present := parse.OptVal("--filter")
	if present {
		filterRegexp = getPattern(s)
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
[SELECTION] msgin [--create] [<INFILE]

Accept a file of messages in RFC822 format representing the
contents of the metadata in selected commits and annotated tags. 
If there is an argument, it will be taken as the name of a message-box
file to read from; if no argument, or one of '-', reads from standard
input. Supports < redirection.  Ordinarily takes no selection set.

Users should be aware that modifying an Event-Number or Event-Mark field
will change which event the update from that message is applied to.  This
is unlikely to have good results.

The header CheckText, if present, is examined to see if the comment
text of the associated event begins with it. If not, the item
modification is aborted. This helps ensure that you are landing
updates on the events you intend.

If the --create modifier is present, new tags and commits will be
appended to the repository.  In this case it is an error for a tag
name to match any existing tag name. Commit events are created with no
fileops.  If Committer-Date or Tagger-Date fields are not present they
are filled in with the time at which this command is executed. If
Committer or Tagger fields are not present, reposurgeon will attempt
to deduce the user's git-style identity and fill it in. If a singleton
commit set was specified for commit creations, the new commits are
made children of that commit.

Otherwise, if the Event-Number and Event-Mark fields are absent, the
msgin logic will attempt to match the commit or tag first by Legacy-ID,
then by a unique committer ID and timestamp pair.

If the option --empty-only is given, this command will throw a recoverable error
if it tries to alter a message body that is neither empty nor consists of the
CVS empty-comment marker.

This operation sets Q bits; true where an object was modified by it, false 
otherwise.
`)
}

// DoMsgin accepts a message-box file representing event metadata and update from it.
func (rs *Reposurgeon) DoMsgin(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdin"})
	defer parse.Closem()
	repo := rs.chosen()
	repo.readMessageBox(rs.selection, parse.stdin,
		parse.options.Contains("--create"),
		parse.options.Contains("--empty-only"))
	if control.isInteractive() {
		changed := repo.countDelFlags()
		if changed == 0 {
			respond("no events modified by msgin.")
		} else {
			respond("%d events modified by msgin.", changed)
		}
	}
	return false
}

// HelpFilter says "Shut up, golint!"
func (rs *Reposurgeon) HelpFilter() {
	rs.helpOutput(`
[SELECTION] filter {dedos|shell|regexp|replace} [TEXT-OR-REGEXP]

Run blobs, commit comments and committer/author names, or tag comments
and tag committer names in the selection set through the filter
specified on the command line.

With any verb other than dedos, attempting to specify a selection
set including both blobs and non-blobs (that is, commits or tags)
throws an error. Inline content in commits is filtered when the
selection set contains (only) blobs and the commit is within the range
bounded by the earliest and latest blob in the specification.

When filtering blobs, if the command line contains the magic cookie
'%PATHS%' it is replaced with a space-separated list of all paths
that reference the blob.

With the verb shell, the remainder of the line specifies a filter as a
shell command. Each blob or comment is presented to the filter on
standard input; the content is replaced with whatever the filter emits
to standard output.

With the verb regex, the remainder of the line is expected to be a Go regular
expression substitution written as /from/to/ with Go-style backslash
escapes interpreted in 'to' as well as 'from'.  Python-style
backreferences (\1, \2 etc.) rather than Go-style $1, $2...  are
interpreted; this avoids a conflict with parameter substitution in
script commands. Any non-space character will work as a delimiter in
place of the /; this makes it easier to use / in patterns. Ordinarily
only the first such substitution is performed; putting 'g' after the
slash replaces globally, and a numeric literal gives the maximum
number of substitutions to perform. Other flags available restrict
substitution scope - 'c' for comment text only, 'C' for committer name
only, 'a' for author names only. See "help regexp" for more information
about regular expressions.

With the verb replace, the behavior is like regexp but the expressions are
not interpreted as regular expressions. (This is slightly faster).

With the verb dedos, DOS/Windows-style \r\n line terminators are replaced with \n.

All variants of this command set Q bits; events actually modified by
the command get true, all other events get false

Some examples:

----
# In all blobs, expand tabs to 8-space tab stops
=B filter shell expand --tabs=8

# Text replacement in comments
=C filter replace /Telperion/Laurelin/c
----
`)
}

type filterCommand struct {
	repo       *Repository
	filtercmd  string
	sub        func(string) string
	regexp     *regexp.Regexp
	attributes orderedStringSet
}

// GoReplacer bridges from Python-style backreferences (\1) to Go-style ($1).
// This was originally a shim for testing during the port from Python.  It has
// been kept because Go's use of $n for group matches conflicts with the
// use of $n for script arguments in reposurgeon.  Also, we can do interpretation
// of Go string escapes at a good point, *after* the Python-style backrefereences
// have been translated.
func GoReplacer(re *regexp.Regexp, fromString, toString string) string {
	for i := 0; i < 10; i++ {
		sdigit := fmt.Sprintf("%d", i)
		toString = strings.Replace(toString, `\`+sdigit, `${`+sdigit+`}`, -1)
		sub, e2 := stringEscape(toString)
		if e2 == nil {
			toString = sub
		}
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
	if strings.HasPrefix(filtercmd, "shell") {
		fc.filtercmd = strings.TrimSpace(filtercmd[5:])
		fc.attributes = newOrderedStringSet("c", "a", "C")
	} else if strings.HasPrefix(filtercmd, "regex") || strings.HasPrefix(filtercmd, "replace") {
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
			if strings.HasPrefix(filtercmd, "regex") {
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
			} else if strings.HasPrefix(filtercmd, "replace") {
				fc.sub = func(s string) string {
					return strings.Replace(s, parts[1], parts[2], subcount)
				}
			} else {
				croak("unexpected verb in filter command")
				return nil
			}
		}
	} else if strings.HasPrefix(filtercmd, "dedos") {
		if len(fc.attributes) == 0 {
			fc.attributes = newOrderedStringSet("c", "a", "C")
		}
		fc.sub = func(s string) string {
			out := strings.Replace(s, "\r\n", "\n", -1)
			return out
		}
	} else {
		croak("missing required command verb")
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

// DoFilter is the handler for the "filter" command.
func (rs *Reposurgeon) DoFilter(line string) (StopOut bool) {
	rs.newLineParse(line, parseREPO|parseNEEDSELECT, nil)
	if line == "" {
		croak("no filter is specified")
		return false
	}
	// Mainline of do_filter() continues {
	filterhook := newFilterCommand(rs.chosen(), line)
	if filterhook != nil {
		rs.chosen().dataTraverse("Filtering",
			rs.selection,
			filterhook.do,
			filterhook.attributes,
			!strings.HasPrefix(line, "dedos"),
			rs.inScript(), control.baton)
	}
	return false
}

// HelpTranscode says "Shut up, golint!"
func (rs *Reposurgeon) HelpTranscode() {
	rs.helpOutput(`
[SELECTION] transcode {ENCODING}

Transcode blobs, commit comments, committer/author names, tag
comments and tag committer names in the selection set to UTF-8 from
the character encoding specified on the command line.

Attempting to specify a selection set including both blobs and
non-blobs (that is, commits or tags) throws an error. Inline content
in commits is filtered when the selection set contains (only) blobs
and the commit is within the range bounded by the earliest and latest
blob in the specification.

The ENCODING argument must name one of the codecs listed at 
https://www.iana.org/assignments/character-sets/character-sets.xhtml
and known to the Go standard codecs library. 

Errors in this command force the repository to be dropped, because an
error may leave repository events in a damaged state.

The theory behind the design of this command is that the
repository might contain a mixture of encodings used to enter commit
metadata by different people at different times. After using "=I" to
identify metadata containing non-Unicode high bytes in text, a human
must use context to identify which particular encodings were used in
particular event spans and compose appropriate transcode commands
to fix them up.

This command sets Q bits; objects actually modified by the command
get true, all other events get false.

----
# In all commit comments containing non-ASCII bytes, transcode from Latin-1.
=I transcode latin1
----

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
		true, !rs.inScript(), control.baton)
	return false
}

// HelpSetfield says "Shut up, golint!"
func (rs *Reposurgeon) HelpSetfield() {
	rs.helpOutput(`
[SELECTION] setfield FIELD VALUE

In the selected events (defaulting to none) set every instance of a
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

// DoSetfield sets an event field from a string.
func (rs *Reposurgeon) DoSetfield(line string) bool {
	rs.newLineParse(line, parseREPO|parseNEEDSELECT, nil)
	repo := rs.chosen()
	fields, err := shlex.Split(line, true)
	if err != nil || len(fields) != 2 {
		croak("missing or malformed setfield line")
	}
	// Calling strings.Title so that Python-style (uncapitalized)
	// fieldnames will still work.
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
SELECTION setperm PERM [PATH...]

For the selected events (defaulting to none) take the first argument as an
octal literal describing permissions.  All subsequent arguments are paths.
For each M fileop in the selection set and exactly matching one of the
paths, patch the permission field to the first argument value.
`)
}

// DoSetperm alters permissions on M fileops matching a path list.
func (rs *Reposurgeon) DoSetperm(line string) bool {
	rs.newLineParse(line, parseREPO|parseNEEDSELECT, nil)
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
[SELECTION] append [--rstrip] TEXT

Append text to the comments of commits and tags in the specified
selection set. The text is the first token of the command and may
be a quoted string. C-style escape sequences in the string are
interpreted using Go's Quote/Unquote codec from the strconv library.

If the option --rstrip is given, the comment is right-stripped before
the new text is appended. If the option --legacy is given, the string
%LEGACY% in the append payload is replaced with the commit's lagacy-ID
before it is appended.

Example:
---------
=C append "\nLegacy-Id: %LEGACY%" --legacy
---------
`)
}

// DoAppend appends a specified line to comments in the specified selection set.
func (rs *Reposurgeon) DoAppend(line string) bool {
	parse := rs.newLineParse(line, parseREPO|parseNEEDSELECT, nil)
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
	parse := rs.newLineParse(line, parseREPO, nil)
	rs.chosen().squash(rs.selection, parse.options, control.baton)
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

Clears all Q bits.
`)
}

// DoDelete is the handler for the "delete" command.
func (rs *Reposurgeon) DoDelete(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	parse.options.Add("--delete")
	rs.chosen().squash(rs.selection, parse.options, control.baton)
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

The default selection set for this command is "=C", all
commits. Occasionally you may want to restrict it, for example to
avoid coalescing unrelated cliques of "empty log message"
commits from CVS lifts.

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
	parse := rs.newLineParse(line, parseALLREPO, nil)
	defer parse.Closem()
	repo := rs.chosen()
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
	modified := repo.doCoalesce(rs.selection, timefuzz, changelog, parse.options.Contains("--debug"), control.baton)
	respond("%d spans coalesced.", modified)
	return false
}

// HelpAdd says "Shut up, golint!"
func (rs *Reposurgeon) HelpAdd() {
	rs.helpOutput(`
SELECTION add { "D" PATH | "M" PERM MARK PATH | "R" SOURCE TARGET | "C" SOURCE TARGET }

In a specified commit, add a specified fileop.

For a D operation to be valid there must be an M operation for the path
in the commit's ancestry.

For an M operation to be valid, PERM must be a token ending with 755
or 644 and the MARK must refer to a blob that precedes the commit
location.  If the MARK is nonexistent or names something other than
a blob, attempting to rebuild a live repository will throw a fatal error.

For an R or C operation to be valid, there must be an M operation
for the SOURCE path in the commit's ancestry.

Some examples:

----
# At commit :15, stop .gitignore from being checked out in later revisions 
:15 add D .gitignore

# Create a new blob :2317 with specified content. At commit :17, add modify 
# or creation of a file named "spaulding>" with its content in the new blob.
# Make it check out with 755 (-rwxr-xr-x) permissions rather than the
# normal 644 (-rw-r--r--). 
blob :2317 <<EOF
Hello, I must be going.
EOF
:17 add M 100755 :2317 spaulding
----
`)
}

// DoAdd adds a fileop to a specified commit.
func (rs *Reposurgeon) DoAdd(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	repo := rs.chosen()
	fields, err := shlex.Split(parse.line, true)
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
		_, err1 := strconv.Atoi(mark[1:])
		if err1 != nil {
			croak("non-numeric mark %s in add command", mark)
			return false
		}
		blob, ok := repo.markToEvent(mark).(*Blob)
		if !ok {
			croak("mark %s in add command does not refer to a blob", mark)
			return false
		} else if repo.eventToIndex(blob) >= rs.selection.Min() {
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
blob [MARK-NUMBER] [<INFILE|>OUTFILE]

Given an argument, create a blob with the specified mark name, which must not
already exist. The new blob is inserted at the front of the repository event
sequence, after options but before previously-existing blobs. The blob data is
taken from standard input, which may be a redirect from a file or a here-doc.

Without an argument return a legal blob name that is not in use, having
a numeric part just one greater than the highest-numbered existing blob in
the repository.  This output may be redirected, but is intended for interactive
use when developing a script.

These commands can be used with the add command to patch new data into a repository.
`)
}

// DoBlob adds a fileop to a specified commit.
func (rs *Reposurgeon) DoBlob(line string) bool {
	parse := rs.newLineParse(line, parseREPO|parseNOSELECT, orderedStringSet{"stdin", "stdout"})
	defer parse.Closem()
	repo := rs.chosen()

	if parse.line == "" {
		var markmax int
		for _, event := range repo.events {
			m := event.getMark()
			if m == "" {
				continue
			}
			v, err := strconv.Atoi(m[1:len(m)])
			if err == nil && v > markmax {
				markmax = v
			}
		}
		fmt.Fprintf(parse.stdout, ":%d\n", markmax+1)
		return false
	} else if !regexp.MustCompile("^:[a-zA-Z0-9]+$").MatchString(parse.line) {
		croak("The mark number (%s) must begin with a colon and contain only numerics.", parse.line)
		return false
	} else if repo.markToEvent(parse.line) != nil {
		croak("Cannot bind blob to existing mark.")
		return false
	}

	blob := newBlob(repo)
	blob.setMark(parse.line)
	repo.insertEvent(blob, len(repo.frontEvents()), "adding blob")
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
[SELECTION] remove [DMRCN] OP [to SELECTION]

From a specified commit, remove a specified fileop. The syntax:

OP must be one of (a) the keyword 'deletes', (b) a file path, (c)
a file path preceded by an op type set (some subset of the letters
DMRCN), or (c) a 1-origin numeric index.  The 'deletes' keyword
selects all D fileops in the commit; the others select one each.

If the to clause is present, the removed op is appended to the
commit specified by the following singleton selection set.  This option
cannot be combined with 'deletes'.
`)
}

// DoRemove deletes a fileop from a specified commit.
func (rs *Reposurgeon) DoRemove(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	if rs.selection == nil {
		rs.selection = newOrderedIntSet()
	}
	repo := rs.chosen()
	orig := parse.line
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
				blob := repo.markToEvent(removed.ref).(*Blob)
				blob.removeOperation(removed)
				// FIXME: Someday, scavenge blos with no references left
				//if len(blob.opset) == 0 {
				//	i := repo.markToIndex(removed.ref)
				//	repo.events = append(repo.events[:i], repo.events[i+1:]...)
				//}
				repo.declareSequenceMutation("scavenging blob")
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

A side effect of this command is to clean up stray "done"
passthroughs that may have entered the repository via graft
operations.  After a renumber, the repository will have at most
one "done", and it will be at the end of the events.
`)
}

// DoRenumber is he handler for the "renumber" command.
func (rs *Reposurgeon) DoRenumber(line string) bool {
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
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
	parse := rs.newLineParse(line, parseALLREPO, nil)
	defer parse.Closem()
	blobMap := make(map[string]string) // hash -> mark
	dupMap := make(map[string]string)  // duplicate mark -> canonical mark
	for _, ei := range rs.selection {
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
	rs.chosen().dedup(dupMap, control.baton)
	return false
}

// HelpTimeoffset says "Shut up, golint!"
func (rs *Reposurgeon) HelpTimeoffset() {
	rs.helpOutput(`
[SELECTION] timeoffset OFFSET

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
	parse := rs.newLineParse(line, parseALLREPO, nil)
	defer parse.Closem()
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
	for _, ei := range rs.selection {
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
func (rs *Reposurgeon) DoWhen(line string) (StopOut bool) {
	rs.newLineParse(line, parseNOSELECT, nil)
	if line == "" {
		croak("a supported date format is required.")
		return false
	}
	d, err := newDate(line)
	if err != nil {
		croak("unrecognized date format")
	} else if strings.Contains(line, "Z") {
		control.baton.printLogString(d.String())
	} else {
		control.baton.printLogString(d.rfc3339())
	}
	return false
}

// HelpDivide says "Shut up, golint!"
func (rs *Reposurgeon) HelpDivide() {
	rs.helpOutput(`
SELECTION divide

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
'qux-late', but the repo is not divided..
`)
}

// DoDivide is the command handler for the "divide" command.
func (rs *Reposurgeon) DoDivide(line string) bool {
	rs.newLineParse(line, parseREPO, nil)
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
[SELECTION] expunge [--not|--notagify] PATH-PATTERN

Expunge files from the selected portion of the repo history; the
default is the entire history.  The argument to this command is a
pattern expression matching paths.

The option --not inverts this; all file paths other than those
selected by the remaining arguments to be expunged.  You may use
this to sift out all file operations matching a pattern set rather
than expunging them.

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
emptycommit-<ident> on the preceding commit unless the --notagify option
is specified.  Commits with deleted fileops pointing both in and outside the
path set are not deleted.

This command sets Q bits: true on any commit which lost fileops but was
not entirely deleted, false on all other events.

Example:

----
# Delete all PDFs from the loaded repository.
expunge /[.]pdf$/
----
`)
}

// DoExpunge expunges files from the chosen repository.
func (rs *Reposurgeon) DoExpunge(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, nil)
	defer parse.Closem()
	fields := parse.Tokens()
	if len(fields) == 0 {
		croak("required argument is missing.")
		return false
	}
	// FIXME: expunge with --not and --notagify needs tests
	expunge := getPattern(fields[0])
	err := rs.chosen().expunge(rs.selection, expunge,
		!parse.options.Contains("--not"), parse.options.Contains("--notagify"), control.baton)
	if err != nil {
		respond(err.Error())
	}
	return false
}

// HelpSplit says "Shut up, golint!"
// FIXME: Odd syntax
func (rs *Reposurgeon) HelpSplit() {
	rs.helpOutput(`
[SELECTION] split [ at M | by PREFIX ]

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

----
reposurgeon% :3 inspect
Event 4 =================================================================
commit refs/heads/master
#legacy-id 2
mark :3
committer brooksd <brooksd> 1353813663 +0000
data 33
add a new file in each directory
M 100644 :1 .gitignore
M 100644 :2 bar/src
M 100644 :2 baz/src
M 100644 :2 foo/src

reposurgeon% :3 split by bar
reposurgeon: new commits are events 4 and 5.

reposurgeon% 4,5 inspect
Event 4 =================================================================
commit refs/heads/master
#legacy-id 2
mark :3
committer brooksd <brooksd> 1353813663 +0000
data 33
add a new file in each directory
M 100644 :1 .gitignore
M 100644 :2 baz/src
M 100644 :2 foo/src

Event 5 =================================================================
commit refs/heads/master
#legacy-id 2.split
mark :7
committer brooksd <brooksd> 1353813663 +0000
data 33
add a new file in each directory
from :3
M 100644 :2 bar/src
----
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

Unite named repositories into one.  Repos need to be loaded (read) first.
They will be processed and removed from the load list.  The union repo
will be selected.

All repos are grafted as branches to the oldest repo.  The branch point
will be the last commit in that repo with a timestamp that is less or
equal to the earliest commit on a grafted branch.

In all repositories but the first, tag and branch duplicate names will be
disambiguated using the source repository name. After all grafts, marks
will be renumbered.

The name of the new repo is composed from names of united repos joined
by '+'. It will have no source directory. The type of repo will be
inherited if all repos share the same type, otherwise no type will be set.

With the option --prune, at each join generate D ops for every
file that doesn't have a modify operation in the root commit of the
branch being grafted on.
`)
}

// DoUnite melds repos together.
func (rs *Reposurgeon) DoUnite(line string) bool {
	rs.unchoose()
	parse := rs.newLineParse(line, parseNOSELECT, nil)
	defer parse.Closem()
	factors := make([]*Repository, 0)
	for _, name := range parse.Tokens() {
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
[SELECTION] graft [--prune] {REPO-NAME}

For when unite doesn't give you enough control. This command may have
either of two forms, distinguished by the size of the selection set.  The
first argument is always required to be the name of a loaded repo.

If the selection set is of size 1, it must identify a single commit in
the currently chosen repo; in this case the named repo's root will
become a child of the specified commit. If the selection set is
empty, the named repo must contain one or more callouts matching
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
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	if len(rs.repolist) == 0 {
		croak("no repositories are loaded.")
		return false
	}
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
debranch SOURCE-BRANCH [TARGET-BRANCH]

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
	parse := rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
	defer parse.Closem()
	args := parse.Tokens()
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
		repo.delete([]int{sourceReset}, nil, control.baton)
	}
	repo.declareSequenceMutation("debranch operation")
	return false
}

// HelpPath says "Shut up, golint!"
func (rs *Reposurgeon) HelpPath() {
	rs.helpOutput(`
path [list [>OUTFILE] | rename PATTERN [--force] TARGET]]

With the verb "list", list all paths touched by fileops in the selection
set (which defaults to the entire repo). This command does > redirection.

With the verb "rename", rename a path in every fileop of every selected
commit.  The default selection set is all commits. The first argument is
interpreted as a pattern expression to match against paths; the second may
contain back-reference syntax (\1 etc.). See "help regexp" for more
information about regular expressions.

Ordinarily, if the target path already exists in the fileops, or is visible
in the ancestry of the commit, this command throws an error.  With the
--force option, these checks are skipped.

Example:

----
# move all content into docs/ subdir
path rename /.+/ docs/\0
----

This command sets commit Q bits; true if the commit was modified.
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

// DoPath renames paths in the history.
func (rs *Reposurgeon) DoPath(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	fields := parse.Tokens()
	if len(fields) == 0 {
		croak("path command requires a verb.")
		return false
	}
	if fields[0] == "rename" {
		if len(fields) != 3 {
			croak("wrong number of fields in path rename command")
			return false
		}
		sourceRE := getPattern(fields[1])
		force := parse.options.Contains("--force")
		targetPattern := fields[2]
		if targetPattern == "" {
			croak("no target specified in path rename")
			return false
		}
		repo.pathRename(rs.selection, sourceRE, targetPattern, force)
	} else if fields[0] == "list" {
		allpaths := newOrderedStringSet()
		for _, commit := range rs.chosen().commits(rs.selection) {
			allpaths = allpaths.Union(commit.paths(nil))
		}
		sort.Strings(allpaths)
		fmt.Fprint(parse.stdout, strings.Join(allpaths, control.lineSep)+control.lineSep)
	} else {
		croak("unknown verb '%s' in path command.", fields[1])
	}
	return false
}

// HelpManifest says "Shut up, golint!"
func (rs *Reposurgeon) HelpManifest() {
	rs.helpOutput(`
[SELECTION] manifest [PATTERN] [>OUTFILE]

Print commit path lists. Takes an optional selection set argument
defaulting to all commits, and an optional pattern expression. For
each commit in the selection set, print the mapping of all paths
in that commit tree to the corresponding blob marks, mirroring what
files would be created in a checkout of the commit. If a regular
expression is given, only print "path -> mark" lines for paths
matching it. See "help regexp" for more information about regular
expressions.
`)
}

// DoManifest prints all files (matching the regex) in the selected commits trees.
func (rs *Reposurgeon) DoManifest(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	var filterFunc = func(s string) bool { return true }
	line = strings.TrimSpace(parse.line)
	if line != "" {
		filterRE := getPattern(line)
		filterFunc = func(s string) bool {
			return filterRE.MatchString(s)
		}
	}
	events := rs.chosen().events
	for _, ei := range rs.selection {
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
[SELECTION] tagify [ --tagify-merges | --canonicalize | --tipdeletes ]

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
makes tagify try harder to detect trivial commits by first removing all
fileops of the selected commits which have no actual effect when processed by
fast-import. For example, file modification ops that don't actually change the
content of the file, or deletion ops that delete a file that doesn't exist in
the parent commit get removed. This rarely happens naturally, but can happen
after some surgical operations, such as reparenting.

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
	parse := rs.newLineParse(line, parseALLREPO, nil)
	defer parse.Closem()
	repo := rs.chosen()
	if parse.line != "" {
		croak("too many arguments for tagify.")
		return false
	}
	before := len(repo.commits(nil))
	err := repo.tagifyEmpty(
		rs.selection,
		parse.options.Contains("--tipdeletes"),
		parse.options.Contains("--tagify-merges"),
		parse.options.Contains("--canonicalize"),
		nil,
		nil,
		true,
		control.baton)
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
SELECTION merge

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
SELECTION unmerge

Linearizes a commit. Takes a selection set argument, which must resolve to a
single commit, and removes all its parents except for the first. It is
equivalent to reparent --rebase {first parent},{commit}, where {commit} is the
selection set given to unmerge and {first parent} is a set resolving to that
commit's first parent, but doesn't need you to find the first parent yourself,
saving time and avoiding errors when nearby surgery would make a manual first
parent argument stale.
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
SELECTION reparent [--user-order] [--rebase]

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

// DoReparent is the ommand handler for the "reparent" command.
func (rs *Reposurgeon) DoReparent(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	defer parse.Closem()
	repo := rs.chosen()
	for _, commit := range repo.commits(nil) {
		commit.invalidateManifests()
	}
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
func (rs *Reposurgeon) DoReorder(line string) bool {
	parse := rs.newLineParse(line, parseREPO|parseNEEDSELECT, nil)
	defer parse.Closem()
	repo := rs.chosen()
	if parse.line != "" {
		croak("'reorder' takes no arguments")
		return false
	}
	commits := repo.commits(rs.selection)
	if len(commits) == 0 {
		croak("no commits in selection")
		return false
	} else if len(commits) == 1 {
		croak("only 1 commit selected; nothing to re-order")
		return false
	} else if len(commits) != len(rs.selection) {
		croak("selection set must be all commits")
		return false
	}
	_, quiet := parse.OptVal("--quiet")

	repo.reorderCommits(rs.selection, quiet)
	return false
}

// HelpBranch says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranch() {
	rs.helpOutput(`
branch {rename|delete} BRANCH-PATTERN [--not] [NEW-NAME]

Rename or delete matching branches. BRANCH-PATTERN can be a string or a regexp.
If it is a string, it should be full reference name like "refs/heads/master".

The command also operates on any associated annotated tags and resets. Git
lightweight tag here is simply a branch in the tags/ namespace.

The --not option inverts a selection for deletion, deleting all branches other
than those matched. Deletion also removes all tags and resets associated with
deleted branches.

For a rename, the third argument may be any token that is a syntactically
valid branch name (but not the name of an existing branch).  If it does not
begin with "refs/", then "refs/" is prepended; you should supply "heads/"
or "tags/" yourself.  In it, references to match parts in BRANCH-PATTERN will
be expanded.

Branch rename has some special behavior when the repository source type is
Subversion. It recognizes tags and resets made from branch-copy commits
and transforms their names as though they were branch fields in commits.

Branch rename sets Q bits; true on every object modified, false otherwise.
`)
}

// DoBranch renames a branch or deletes it.
func (rs *Reposurgeon) DoBranch(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, nil)

	repo := rs.chosen()

	removeBranchPrefix := func(branch string) string {
		if strings.HasPrefix(branch, "refs/") {
			branch = branch[5:]
		}
		return branch
	}
	addBranchPrefix := func(branch string) string {
		if !strings.HasPrefix(branch, "refs/") {
			return "refs/" + branch
		}
		return branch
	}

	var verb string
	verb, line = popToken(line)
	if verb == "" {
		croak("branch command requires a verb.")
		return false
	}

	if verb == "rename" {
		sourcepattern, line := popToken(line)
		var err error
		if err != nil {
			croak("while selecting branch: %v", err)
			return false
		}

		var newname string
		newname, line = popToken(line)
		if newname == "" {
			croak("branch name must be nonempty.")
			return false
		}
		newname = removeBranchPrefix(newname)
		sourcepattern = removeBranchPrefix(sourcepattern)
		sourceRE := getPattern(sourcepattern)
		repo.clrDelFlags()
		for _, branch := range repo.branchset() {
			branch := removeBranchPrefix(branch)
			if !sourceRE.MatchString(branch) {
				continue
			}
			subst := GoReplacer(sourceRE, branch, newname)
			if repo.branchset().Contains(addBranchPrefix(subst)) {
				croak("there is already a branch named 'refs/heads/%s'.", subst)
				return false
			}
			for _, event := range repo.events {
				if commit, ok := event.(*Commit); ok {
					if commit.Branch == addBranchPrefix(branch) {
						commit.setBranch(addBranchPrefix(subst))
						commit.setDelFlag(true)
					}
				} else if reset, ok := event.(*Reset); ok {
					if reset.ref == addBranchPrefix(branch) {
						reset.ref = addBranchPrefix(subst)
						reset.setDelFlag(true)
					}
				}
			}
		}
		// Things get a little weird and kludgy here. It's the
		// price we gave to pay for deferring Subversion
		// branch remapping to be done in gitspace rather than
		// as a phase in the Subversion reader.
		//
		// What we're coping with is the possibility of tags
		// and resets that were made from Subversion
		// branch-copy commits. The name and ref fields of
		// such things are branch IDs with the suffix "-root",
		// but without a refs/heads leader, and we need tp
		// put the prefix part through the the same
		// transformation as branch names.
		//
		// This pass depends on the fact that we've already done
		// collision checks for all branch renames.
		if repo.vcs != nil && repo.vcs.name == "svn" {
			for _, event := range repo.events {
				if tag, ok := event.(*Tag); ok {
					if !strings.HasSuffix(tag.tagname, "-root") {
						continue
					}
					tagname := removeBranchPrefix(tag.tagname)
					tagname = tagname[:len(tagname)-5]
					tagname = "heads/" + tagname
					if !sourceRE.MatchString(tagname) {
						continue
					}
					subst := GoReplacer(sourceRE, tagname, newname)
					tag.tagname = subst[6:] + "-root"
					tag.setDelFlag(true)
				} else if reset, ok := event.(*Reset); ok {
					resetname := removeBranchPrefix(reset.ref)
					if !sourceRE.MatchString(resetname) {
						continue
					}
					subst := GoReplacer(sourceRE, resetname, newname)
					reset.ref = addBranchPrefix(subst)
					reset.setDelFlag(true)
				}
			}
		}
		if n := repo.countDelFlags(); n == 0 {
			croak("no branch fields matched %s", sourceRE)
		} else {
			respond("%d objects modified", n)
		}
	} else if verb == "delete" {
		sourcepattern, _ := popToken(line)
		var err error
		if err != nil {
			croak("while selecting branch: %v", err)
			return false
		}

		var shouldDelete func(string) bool
		sourcepattern, isRe := delimitedRegexp(sourcepattern)
		if !isRe {
			sourcepattern = "^" + regexp.QuoteMeta(addBranchPrefix(sourcepattern)) + "$"
		}
		branchRE, err := regexp.Compile(sourcepattern)
		if err != nil {
			croak("in branch command: %v", err)
			return false
		}
		shouldDelete = func(branch string) bool {
			return branchRE.MatchString(branch) == !parse.options.Contains("--not")
		}
		before := len(repo.branchset())
		repo.deleteBranch(shouldDelete, control.baton)
		respond("%d branches deleted", before-len(repo.branchset()))
	} else {
		croak("unknown verb '%s' in branch command.", verb)
		return false
	}

	return false
}

// HelpTag says "Shut up, golint!"
func (rs *Reposurgeon) HelpTag() {
	rs.helpOutput(`
[SELECTION] tag {create|move|rename|delete} [TAG-PATTERN] [--not] [NEW-NAME|SINGLETON]

Create, move, rename, or delete annotated tags.

Creation is a special case.  First argument is NEW-NAME, which must not
be an existing tag. Takes a singleton event second argument which must
point to a commit.  A tag event pointing to the commit is created and
inserted just after the last tag in the repo (or just after the last
commit if there are no tags).  The tagger, committish, and comment
fields are copied from the commit's committer, mark, and comment
fields.

Otherwise, the TAG-PATTERN argument is a pattern expression matching
a set of tags.  The subcommand must be one of the verbs 'move',
'rename', or 'delete'.

For a 'move', a second argument must be a singleton selection set. For
a 'rename', the third argument may be any token that is a syntactically
valid tag name (but not the name of an existing tag).  When TAG-PATTERN
is a regexp, NEW-NAME may contain references to portions of the match.
Errors are thrown for wildcarding that would produce name collisions

For a 'delete', no second argument is required.  Annotated tags with names
matching the pattern are deleted.  Giving a regular expression rather than
a plain string is useful for mass deletion of junk tags such as those derived
from CVS branch-root tags. Such deletions can be restricted by a selection
set in the normal way.

All Q bits are cleared; then any tags made by create or touched by move or
rename, get their Q bit set.
`)
}

// DoTag moves a tag to point to a specified commit, or renames it, or deletes it.
func (rs *Reposurgeon) DoTag(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, nil)
	repo := rs.chosen()
	var verb string
	verb, line = popToken(line)
	if verb == "" {
		croak("tag command requires a verb.")
		return false
	}

	repo.clrDelFlags()
	if verb == "create" {
		var tagname string
		tagname, line = popToken(line)
		if len(tagname) == 0 {
			croak("missing tag name")
			return false
		}
		var ok bool
		var target *Commit
		if repo.named(tagname) != nil {
			croak("something is already named %s", tagname)
			return false
		}
		rs.setSelectionSet(line)
		if rs.selection == nil {
			croak("usage: tag create <name> <singleton-selection>")
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
		tag.setDelFlag(true)
		control.baton.twirl()
		return false
	}

	sourcepattern, line := popToken(line)
	if len(sourcepattern) == 0 {
		croak("missing tag pattern")
		return false
	}
	sourcepattern, isRe := delimitedRegexp(sourcepattern)
	if !isRe {
		sourcepattern = "^" + regexp.QuoteMeta(sourcepattern) + "$"
	} else if verb == "create" {
		croak("cannot reset create from a regular expression")
		return false
	}
	sourceRE, err := regexp.Compile(sourcepattern)
	if err != nil {
		croak(err.Error())
		return false
	}

	// Collect all matching tags in the selection set
	tags := make([]*Tag, 0)
	for _, idx := range rs.selection {
		event := repo.events[idx]
		if tag, ok := event.(*Tag); ok && sourceRE.MatchString(tag.tagname) == !parse.options.Contains("--not") {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		croak("no tag matches %s.", sourcepattern)
		return false
	}

	// Validate the operation
	var target *Commit
	var newname string
	if verb == "move" {
		var ok bool
		rs.setSelectionSet(line)
		if len(rs.selection) != 1 {
			croak("tag move requires a singleton commit set.")
			return false
		} else if target, ok = repo.events[rs.selection[0]].(*Commit); !ok {
			croak("move target is not a commit.")
			return false
		}
	} else if verb == "rename" {
		newname, line = popToken(line)
		if newname == "" {
			croak("new tag name must be nonempty.")
			return false
		}
		if repo.named(newname) != nil {
			croak("something is already named %s", newname)
			return false
		}
	} else if verb != "delete" {
		croak("unknown verb '%s' in tag command.", verb)
		return false
	}

	// Do it
	control.baton.startProcess("tag"+verb, "")
	for i, tag := range tags {
		if verb == "move" {
			tag.forget()
			tag.remember(repo, target.mark)
			tag.setDelFlag(true)
		} else if verb == "rename" {
			possible := GoReplacer(sourceRE, tag.tagname, newname)
			if i > 0 && possible == tags[i-1].tagname {
				croak("tag name collision, not renaming.")
				return false
			}
			tag.tagname = possible
			tag.setDelFlag(true)
		} else if verb == "delete" {
			// the order here in important
			repo.delete([]int{tag.index()}, nil, control.baton)
			tag.forget()
		}
		control.baton.twirl()
	}
	if verb != "delete" {
		if n := repo.countDelFlags(); n == 0 {
			croak("no tag names matched %s", sourceRE)
		} else {
			respond("%d objects modified", n)
		}
	}
	control.baton.endProcess()
	if verb == "delete" {
		repo.declareSequenceMutation("tag deletion")
	}

	return false
}

// HelpReset says "Shut up, golint!"
func (rs *Reposurgeon) HelpReset() {
	rs.helpOutput(`
[SELECTION] reset {create|move|rename|delete} [RESET-PATTERN] [--not] [NEW-NAME]

Note: While this command is provided for the sake of completeness, think
twice before actually using it.  Normally a reset should only be deleted
or renamed when its associated branch is, and the branch command does this.

Create, move, rename, or delete resets. Create is a special case; it
requires a singleton selection which is the associated commit for the
reset, takes as a first argument the name of the reset (which must not
exist), and ends with the keyword create. In this case the name must be 
fully qualified, with a refs/heads/ or refs/tags/ prefix.

In the other modes, the RESET-PATTERN finds by text match existing resets 
within the selection.  If RESET-PATTERN is a delimited regexp, the match is
to the regexp (--not inverts this, selecting all non-matching resets). 

If RESET-PATTERN is a text literal, each reset's name is matched if 
RESET-PATTERN is either the entire reference (refs/heads/FOO or refs/tags/FOO
for some some valueof FOO) or the basename (e.g. FOO), or a suffix of the form
heads/FOO or tags/FOO. An unqualified basename is assumed to refer to a branch
in refs/heads/.

The second argument must be one of the verbs 'move', 'rename', or 'delete'.
The default selection is all events.

For a 'move', a SINGLETON argument must be a singleton selection set. For
a 'rename', the third argument may be any token that can be interpreted
as a valid reset name (but not the name of an existing
reset). For a 'delete', no third argument is required.

When a reset is renamed, commit branch fields matching the tag are
renamed with it to match.  When a reset is deleted, matching branch
fields are changed to match the branch of the unique descendant of the
tip commit of the associated branch, if there is one.  When a reset is
moved, no branch fields are changed.

All Q bits are cleared; then any resets made by create, or touched by move
or rename, get their Q bit set.
`)
}

// DoReset moves a reset to point to a specified commit, or renames it, or deletes it.
func (rs *Reposurgeon) DoReset(line string) bool {
	parse := rs.newLineParse(line, parseREPO, nil)
	repo := rs.chosen()

	var verb string
	verb, line = popToken(line)
	if verb == "" {
		croak("reset command requires a verb.")
		return false
	}

	var err error

	resetname, line := popToken(line)
	if len(resetname) == 0 {
		croak("missing reset pattern")
		return false
	}
	sourcepattern, isRe := delimitedRegexp(resetname)
	if isRe {
		if verb == "create" {
			croak("cannot reset create from a regular expression")
			return false
		}
	} else {
		if !strings.Contains(sourcepattern, "/") {
			sourcepattern = "heads/" + sourcepattern
		}
		if !strings.HasPrefix(sourcepattern, "refs/") {
			sourcepattern = "refs/" + sourcepattern
		}
		sourcepattern = "^" + regexp.QuoteMeta(sourcepattern) + "$"
	}
	sourceRE, err := regexp.Compile(sourcepattern)
	if err != nil {
		croak(err.Error())
		return false
	}

	repo.clrDelFlags()
	resets := make([]*Reset, 0)
	selection := rs.selection
	if selection == nil {
		selection = rs.repo.all()
	}
	for _, ei := range selection {
		reset, ok := repo.events[ei].(*Reset)
		if ok && sourceRE.MatchString(reset.ref) == !parse.options.Contains("--not") {
			resets = append(resets, reset)
		}
	}
	if verb == "create" {
		var target *Commit
		var ok bool
		if len(resets) > 0 {
			croak("one or more resets match %s", sourcepattern)
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
		reset.setDelFlag(true)
		repo.addEvent(reset)
		repo.declareSequenceMutation("reset create")
	} else if verb == "move" {
		var reset *Reset
		var target *Commit
		var ok bool
		if len(resets) == 0 {
			croak("no such reset as %s", sourcepattern)
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
		reset.setDelFlag(true)
		repo.declareSequenceMutation("reset move")
	} else if verb == "rename" {
		var newname string
		if len(resets) == 0 {
			croak("no resets match %s", sourcepattern)
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
			reset.setDelFlag(true)
		}
		for _, commit := range repo.commits(nil) {
			if sourceRE.MatchString(commit.Branch) {
				commit.Branch = newname
				commit.setDelFlag(true)
			}
		}
	} else if verb == "delete" {
		if len(resets) == 0 {
			croak("no resets match %s", sourcepattern)
			return false
		}
		var tip *Commit
		for _, commit := range repo.commits(nil) {
			if sourceRE.MatchString(commit.Branch) {
				tip = commit
			}
		}
		if tip != nil && len(tip.children()) == 1 {
			successor := tip.children()[0]
			if cSuccessor, ok := successor.(*Commit); ok {
				for _, commit := range repo.commits(nil) {
					if sourceRE.MatchString(commit.Branch) {
						commit.Branch = cSuccessor.Branch
					}
				}
			}
		}
		for _, reset := range resets {
			reset.forget()
			repo.delete([]int{repo.eventToIndex(reset)}, nil, control.baton)
		}
		repo.declareSequenceMutation("reset delete")
	} else {
		croak("unknown verb '%s' in reset command.", verb)
	}
	if verb != "delete" {
		if n := repo.countDelFlags(); n == 0 {
			croak("no reset names matched %s", sourceRE)
		} else {
			respond("%d objects modified", n)
		}
	}
	return false
}

// HelpBranchlift says "Shut up, golint!"
func (rs *Reposurgeon) HelpBranchlift() {
	rs.helpOutput(`
branchlift {SOURCEBRANCH} {PATHPREFIX} [NEWNAME]

Every commit on SOURCEBRANCH with fileops matching the PATHPREFIX is examined;
all commits with every fileop matching the PATH are moved to a new branch; if
a commit has only some matching fileops it is split and the fragment containing
the matching fileops is moved.

Every matching commit is modified to have the branch label specified by NEWNAME. 
If NEWNAME is not specified, the basename of PATHPREFIX is used.  If the resulting
branch already exists, this command errors out without modifying the repository. 

The PATHPREFIX is removed from the paths of all fileops in modified commits.

Backslash escapes are processed in all three names.

Sets Q bits: commits on the source branch modified by having fileops lifted to the 
new branch true, all others false.
`)
}

// DoBranchlift lifts a directory to become a branch
func (rs *Reposurgeon) DoBranchlift(line string) bool {
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)

	repo := rs.chosen()

	// We need a source branch
	sourcebranch, line := popToken(line)
	var err error
	sourcebranch, err = stringEscape(sourcebranch)
	if err != nil {
		croak("while selecting branch: %v", err)
		return false
	}
	if !strings.HasPrefix(sourcebranch, "refs/heads/") {
		sourcebranch = "refs/heads/" + sourcebranch
	}
	if !repo.branchset().Contains(sourcebranch) {
		croak("no such branch as %s", sourcebranch)
		return false
	}

	// We need a path prefix
	var pathprefix string
	pathprefix, line = popToken(line)
	if pathprefix == "" || pathprefix == "." || pathprefix == "/" {
		croak("path prefix argument must be nonempty and not . or /.")
		return false
	}
	if !strings.HasSuffix(pathprefix, "/") {
		pathprefix += "/"
	}

	// We need a new branch name
	var newname string
	newname, line = popToken(line)
	if newname == "" {
		newname = path.Base(pathprefix[:len(pathprefix)-1])
	}
	if !strings.HasPrefix(newname, "refs/heads/") {
		newname = "refs/heads/" + newname
	}
	if repo.branchset().Contains(newname) {
		croak("there is already a branch named '%s'.", newname)
		return false
	}

	if splitcount := repo.branchlift(sourcebranch, pathprefix, newname); splitcount == -1 {
		croak("branchlift internal error %q - repo may be garbled!", err)
		return false
	} else if splitcount > 0 {
		respond("%d commits were split while lifting %s", splitcount, pathprefix)
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
	rs.newLineParse(line, parseREPO|parseNOSELECT, nil)
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
// FIXME: Odd syntax
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
the name, and '/e' to only the email address. See "help regexp" for more
information about regular expressions.

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
    To change a committer or tagger, use 'setfield' instead.

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
    To change a committer or tagger, use 'setfield' instead.

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
	parse := rs.newLineParse(rest, parseNONE, orderedStringSet{"stdout"})
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
[SELECTION] authors {read <INFILE | write >OUTFILE}

Apply or dump author-map information for the specified selection
set, defaulting to all events.

Lifts from CVS and Subversion may have only usernames local to
the repository host in committer and author IDs. DVCSes want email
addresses (net-wide identifiers) and complete names. To supply the map
from one to the other, an authors file is expected to consist of
lines each beginning with a local user ID, followed by a '=' (possibly
surrounded by whitespace) followed by a full name and email address. Thus:

--------
fred = Fred J. Foonly <foonly@foo.com> America/New_York
--------

An authors file may also contain lines of this form

--------
+ Fred J. Foonly <foonly@foobar.com> America/Los_Angeles
--------

These are interpreted as aliases for the last preceding =
entry that may appear in ChangeLog files. When such an alias is
matched on a ChangeLog attribution line, the author attribution
for the commit is mapped to the basename, but the timezone is used
as is.  This accommodates people with past addresses (possibly at
different locations) unifying such aliases in metadata so searches
and statistical aggregation will work better.

An authors file may have comment lines beginning with #; these
are ignored.

When an authors file is applied, email addresses in committer and author
metadata for which the local ID matches between &lt; and @ are replaced
according to the mapping (this handles git-svn lifts). Alternatively,
if the local ID is the entire address, this is also considered a match
(this handles what git-cvsimport and cvs2git do). If a timezone was
specified in the map entry, that person's author and committer dates
are mapped to it.

With the 'read' modifier, apply author mapping data (from standard input
or a <-redirected input file).  Q bits are set: true on each commit event 
with attributions actually modified by the mapping, false on all other
events.

With the 'write' modifier, write a mapping file that could be
interpreted by 'authors read', with entries for each unique committer,
author, and tagger (to standard output or a >-redirected file). This
may be helpful as a start on building an authors file, though each
part to the right of an equals sign will need editing.
`)
}

// DoAuthors applies or dumps author-mapping file.
func (rs *Reposurgeon) DoAuthors(line string) bool {
	selection := rs.selection
	if selection == nil {
		selection = rs.chosen().all()
	}
	if strings.HasPrefix(line, "write") {
		line = strings.TrimSpace(line[5:])
		parse := rs.newLineParse(line, parseREPO|parseNEEDREDIRECT, orderedStringSet{"stdout"})
		defer parse.Closem()
		rs.chosen().writeAuthorMap(selection, parse.stdout)
	} else if strings.HasPrefix(line, "read") {
		line = strings.TrimSpace(line[4:])
		parse := rs.newLineParse(line, parseREPO|parseNEEDREDIRECT, orderedStringSet{"stdin"})
		defer parse.Closem()
		rs.chosen().readAuthorMap(selection, parse.stdin)
	} else {
		croak("ill-formed authors command")
	}
	return false
}

//
// Reference lifting
//

// HelpLegacy says "Shut up, golint!"
func (rs *Reposurgeon) HelpLegacy() {
	rs.helpOutput(`
legacy {read [<INFILE] | write [>OUTFILE]}

Apply or list legacy-reference information. Does not take a
selection set. The 'read' variant reads from standard input or a
<-redirected filename; the 'write' variant writes to standard
output or a >-redirected filename.
`)
}

// DoLegacy apply a reference-mapping file.
func (rs *Reposurgeon) DoLegacy(line string) bool {
	if strings.HasPrefix(line, "write") {
		line = strings.TrimSpace(line[5:])
		parse := rs.newLineParse(line, parseREPO|parseNEEDREDIRECT, orderedStringSet{"stdout"})
		defer parse.Closem()
		rs.chosen().writeLegacyMap(parse.stdout, control.baton)
	} else if strings.HasPrefix(line, "read") {
		line = strings.TrimSpace(line[4:])
		parse := rs.newLineParse(line, parseREPO|parseNEEDREDIRECT, []string{"stdin"})
		defer parse.Closem()
		rs.chosen().readLegacyMap(parse.stdin, control.baton)
	} else {
		croak("ill-formed legacy command")
	}
	return false
}

// HelpReferences says "Shut up, golint!"
func (rs *Reposurgeon) HelpReferences() {
	rs.helpOutput(`
[SELECTION] references [list|lift]

With the 'list' modifier, produces a listing of events that may have
Subversion or CVS commit references in them.  This version
of the command supports > redirection.  Equivalent to '=N list'.

With the modifier 'lift', transform commit-reference cookies from CVS
and Subversion into action stamps.  This command expects cookies
consisting of the leading string '[[', followed by a VCS identifier
(currently SVN or CVS) followed by VCS-dependent information, followed
by ']]'. An action stamp pointing at the corresponding commit is
substituted when possible.  Enables writing of the legacy-reference
map when the repo is written or rebuilt.  This variant sets Q bits:
true if a commit's comment was modified by a reference lift, false
on all other events.
`)
}

// DoReferences looks for things that might be CVS or Subversion revision references.
func (rs *Reposurgeon) DoReferences(line string) bool {
	rs.newLineParse(line, parseALLREPO, nil)
	repo := rs.chosen()
	if strings.Contains(line, "lift") {
		repo.clrDelFlags()
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
			for _, commit := range rs.chosen().commits(rs.selection) {
				oldcomment := commit.Comment
				commit.Comment = matchRE.ReplaceAllStringFunc(
					commit.Comment,
					func(m string) string {
						return substitute(item.getter, m)
					})
				if commit.Comment != oldcomment {
					commit.setDelFlag(true)
				}
			}
		}
		respond("%d references resolved.", hits)
		repo.writeLegacy = true
	} else {
		selection := make([]int, 0)
		for idx, commit := range repo.commits(nil) {
			if rs.hasReference(commit) {
				selection = append(selection, idx)
			}
		}
		if len(selection) > 0 {
			parse := rs.newLineParse(line, parseNONE, orderedStringSet{"stdout"})
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

Sets Q bits: true for each commit and tag with a comment modified by this
command, false on all other events.
`)
}

// DoGitify canonicalizes comments.
func (rs *Reposurgeon) DoGitify(line string) bool {
	rs.newLineParse(line, parseALLREPO, nil)
	lineEnders := orderedStringSet{".", ",", ";", ":", "?", "!"}
	control.baton.startProgress("gitifying comments", uint64(len(rs.selection)))
	rs.chosen().clrDelFlags()
	rs.chosen().walkEvents(rs.selection, func(idx int, event Event) {
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
				commit.setDelFlag(true)
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
				if commit != nil {
					commit.setDelFlag(true)
				}
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
SELECTION checkout

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
SELECTION diff [>OUTFILE]

Display the difference between commits. Takes a selection-set argument which
must resolve to exactly two commits.
`)
}

// DoDiff displays a diff between versions.
func (rs *Reposurgeon) DoDiff(line string) bool {
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
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
set [canonicalize|crlf|compress|echo|experimental|interactive|progress|serial|testmode|quiet]+

Set a (tab-completed) boolean option to control reposurgeon's
behavior.  With no arguments, displays the state of all flags.
Do "help options" to see the available options.
`)
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
	rs.newLineParse(line, parseNOSELECT, nil)
	tweakFlagOptions(line, true)
	return false
}

// HelpClear says "Shut up, golint!"
func (rs *Reposurgeon) HelpClear() {
	rs.helpOutput(`
clear [canonicalize|crlf|compress|echo|experimental|interactive|progress|serial|testmode|quiet]+

Clear a (tab-completed) boolean option to control reposurgeon's
behavior.  With no arguments, displays the state of all flags.
Do "help options" to see the available options.
`)
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
	rs.newLineParse(line, parseNOSELECT, nil)
	tweakFlagOptions(line, false)
	return false
}

// HelpReadLimit says "Shut up, golint!"
func (rs *Reposurgeon) HelpReadLimit() {
	rs.helpOutput(`
readlimit N

Set a maximum number of commits to read from a stream.  If the limit
is reached before EOF it will be logged. Mainly useful for benchmarking.
Without arguments, report the read limit; 0 means there is none.
`)
}

// DoReadlimit is the command handler for the "readlimit" command.
func (rs *Reposurgeon) DoReadlimit(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
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
func (rs *Reposurgeon) DoDefine(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
	words := strings.SplitN(line, " ", 2)
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
do MACRO-NAME [ARG...]

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
	parse := rs.newLineParse(line, parseNONE, orderedStringSet{"stdout"})
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
undefine MACRO-NAME

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
	rs.newLineParse(line, parseNOSELECT, nil)
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

This command sets Q bits: true on each event with a timestamp bumped, false on
all other events.
`)
}

// DoTimequake is the handler for the "timequake" command.
func (rs *Reposurgeon) DoTimequake(line string) bool {
	rs.newLineParse(line, parseALLREPO, nil)
	repo := rs.chosen()
	//baton.startProcess("reposurgeon: disambiguating", "")
	modified := 0
	repo.clrDelFlags()
	for _, event := range repo.commits(rs.selection) {
		parents := event.parents()
		if len(parents) == 1 {
			if parent, ok := parents[0].(*Commit); ok {
				if event.committer.date.timestamp.Equal(parent.committer.date.timestamp) {
					event.bump(1)
					event.setDelFlag(true)
					modified++
				}
			}
		}
		//baton.twirl()
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
[SELECTION] changelogs [BASENAME-PATTERN]

Mine ChangeLog files for authorship data.

Takes a selection set.  If no set is specified, process all changelogs.
An optional following argument is a pattern expression to match the 
basename of files that should be treated as changelogs; the default
is "/^ChangeLog$/". See "help regexp" for more information about
regular expressions.

This command assumes that changelogs are in the format used by FSF
projects: entry header lines begin with YYYY-MM-DD and are followed by
a fullname/address.

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

Sets Q bits: true if the event is a commit with authorship modified
by this command, false otherwise.
`)
}

// DoChangelogs mines repository changelogs for authorship data.
func (rs *Reposurgeon) DoChangelogs(line string) bool {
	rs.newLineParse(line, parseALLREPO, nil)
	ok, cm, cc, cd, cl := rs.chosen().processChangelogs(rs.selection, line, control.baton)
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
{SELECTION} incorporate [--date=YY-MM-DDTHH:MM:SS|--after|--firewall] [TARBALL...]

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
	parse := rs.newLineParse(line, parseREPO, orderedStringSet{"stdin"})
	defer parse.Closem()
	repo := rs.chosen()
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
	}
	date, _ := parse.OptVal("--date")
	repo.doIncorporate(tarballs, commit, strip, parse.options.Contains("--firewall"), parse.options.Contains("--after"), date)
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

It is good practice to start your lift script with a version
requirement, especially if you are going to archive it for later
reference.
`)
}

// DoVersion is the handler for the "version" command.
func (rs *Reposurgeon) DoVersion(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, orderedStringSet{"stdout"})
	defer parse.Closem()
	if line == "" {
		// This is a technically wrong way of enumerting the list and will need to
		// change if we ever have visible extractors not corresponding to an
		// entry in the base VCS table.
		supported := make([]string, 0)
		for _, v := range vcstypes {
			supported = append(supported, v.name)
		}
		sort.Strings(supported)
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
elapsed [>OUTFILE]

Display elapsed time since start.
`)
}

// DoElapsed is the handler for the "elapsed" command.
func (rs *Reposurgeon) DoElapsed(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, orderedStringSet{"stdout"})
	defer parse.Closem()
	parse.respond("elapsed time %v.", time.Now().Sub(rs.startTime))
	return false
}

// HelpExit says "Shut up, golint!"
func (rs *Reposurgeon) HelpExit() {
	rs.helpOutput(`
exit [>OUTFILE]

Exit cleanly, emitting a goodbye message including elapsed time.
`)
}

// DoExit is the handler for the "exit" command.
func (rs *Reposurgeon) DoExit(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, orderedStringSet{"stdout"})
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

Follow with space and a command name to show help for the command.

Without an argument, list help topics.

"?" is a shortcut synonym for "help".

If required, and $PAGER is set, help items long enough to need it
will be fed to that pager for display.
`)
}

// HelpSelection says "Shut up, golint!"
func (rs *Reposurgeon) HelpSelection() {
	rs.helpOutputMisc(`
A quick example-centered reference for selection-set syntax.

First, these ways of constructing singleton sets:

----
123        event numbered 123 (1-origin)
:345       event with mark 345
<456>      commit with legacy-ID 456 (probably a Subversion revision)
<foo>      the tag named 'foo', or failing that the tip commit of branch foo
----

You can select commits and tags by date, or by date and committer:

----
<2011-05-25>                  all commits and tags with this date
<2011-05-25!esr>              all with this date and committer
<2011-05-25T07:30:37Z>        all commits and tags with this date and time
<2011-05-25T07:30:37Z!esr>    all with this date and time and committer
<2011-05-25T07:30:37Z!esr#2>  event #2 (1-origin) in the above set
----

More ways to construct event sets:

----
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
=Q         all events marked with the "recently touched" bit.

@min()     create singleton set of the least element in the argument
@max()     create singleton set of the greatest element in the argument
----

Other special functions are available: do 'help functions' for more.

You can compose sets as follows:

----
:123,<foo>     the event marked 123 and the event referenced by 'foo'.
:123..<foo>    the range of events from mark 123 to the reference 'foo'
----

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
	rs.helpOutputMisc(`
Each command description begins with a syntax summary.  Mandatory parts are
in {}, optional in [], and ... says the element just before it may be repeated.
Parts in ALL-CAPS are expected to be filled in by the user.

Commands are distinguished by a command keyword.  Most take a selection set
immediately before it; see "help selection" for details.  Some commands
have a following subcommand verb. Many commands take additional modifier
arguments after the command keyword(s).

The syntax of following arguments is variable according to the
requirements of individual commands, but there are a couple of general
rules.

* You can have comments in a script, led by the character "#".  Both
  whole-line and "winged" comments following command arguments are
  supported.  Note that reposurgeon's command parser is fairly
  primitive and will be confused by a literal # in a command argument.

* Many commands interpret C/Go style backslash escapes like \n in
  arguments.  You can usually, for example, get around having to
  include a literal # in an argument by writing "\x23".

* Some commands support option flags.  These are led with a --, so if
  there is an option flag named "foo" you would write it as "--foo".
  Option flags are parsed out of the command line before any other
  interpretation is performed, and can be anywhere on the line.  The
  order of option flags is never significant.

* When an option flag "foo" sets a value, the syntax is --foo=xxx
  with no spaces around the equal sign.

* All commands that expect data to be presented on standard input support
  input redirection.  You may write "<myfile" to take input from the
  file named "myfile".  Redirections are parsed out early, before
  the command arguments proper are interpreted, and can be anywhere
  on the line

* All commands that expect data to be presented on standard input also
  accept a here-document, just the shell syntax for here-documents
  with a leading "<<". There are two here-documents in the quick-start
  example.

* Most commands that normally ship data to standard output accept
  output redirection.  As in the shell, you can write ">outfile" to
  send the command output to "outfile", and ">>outfile2" to append
  to outfile2.

* Some commands take following arguments that are regular
  expressions. In this context, they still require start and end
  delimiters as they do when used in a selection prefix, but if you
  need to have a / in the expression the delimiters can be any
  punctuation character other than an ASCII single quote.  As a
  reminder, these are described in the embedded help as pattern
  expressions.

* Also note that following-argument regular expressions may not contain
  whitespace; if you need to specify whitespace or a non-printable
  character use a standard C-style escape such as "\s" for space.
`)
}

// HelpRedirection says "Shut up, golint!"
func (rs *Reposurgeon) HelpRedirection() {
	rs.helpOutputMisc(`
An optional command argument prefixed by "<" indicates that the command
accepts input redirection; an optional argument prefixed by ">"
indicates that the command accepts output redirection. There
must be whitespace before the "<" or ">" so that the command parser
won't falsely match uses of these characters in regular expressions.

Commands that support output redirection can also be followed by a
pipe bar and a normal Unix command.  For example, "list | more"
directs the output of a list command to more(1).  Some whitespace
around the pipe bar is required to distinguish it from uses
of the same character as the alternation operator in regular
expressions.

The command line following the first pipe bar, if present, is
passed to a shell and may contain a general shell command line,
including more pipe bars. The SHELL environment variable can
set the shell, falling back to /bin/sh.

Beware that while the reposurgeon CLI mimics these simple shell
features, many things you can do in a real shell won't work until the
right-hand side of a pipe-bar output redirection, if there is one.
String-quoting arguments will fail unless the specific, documented
syntax of a command supports that.  You can't redirect standard error
(but see the "log" command for a rough equivalent). And you
can't pipe input from a shell command.

In general you should avoid trying to get cute with the command parser.
It's stupider than it looks.
`)
}

// HelpFunctions says "Shut up, golint!"
func (rs *Reposurgeon) HelpFunctions() {
	rs.helpOutputMisc(`
The selection-expression language has named special functions.  The syntax
for a named function is "@" followed by a function name,
followed by an argument in parentheses. Presently the following
functions are defined:

|===================================================================
| @min() | create singleton set of the least element in the argument
| @max() | create singleton set of the greatest element in the argument
| @amp() | nonempty selection set becomes all events, empty set is returned
| @par() | all parents of commits in the argument set
| @chn() | all children of commits in the argument set
| @dsc() | all commits descended from the argument set (argument set included)
| @anc() | all commits ancestral to the argument set (argument set included)
| @pre() | events before the argument set
| @suc() | events after the argument set
| @srt() | sort the argument set by event number.
|===================================================================
`)
}

// HelpRegexp says "Shut up, golint!"
func (rs *Reposurgeon) HelpRegexp() {
	rs.helpOutputMisc(`
The pattern expressions used in event selections and various commands
(attribution, expunge, filter, msgout, path) are those of the Go 
language, with one exception. Due to a conflict with the use of $
for arguments in the "script" command, we retain Python's use of
backslashes as a leader for references to group matches.

Normally patterns intended to be interpreted as regular expressions are 
wrapped in slashes (e.g. /foobar/ matches any text containing the string
"foobar"), but any punctuation character other than single quote will work
as a delimiter in place of the /; this makes it easier to use an actual /
in patterns.  Matched single quote delimiters mean the literal should be
interpreted as plain text, suppressing interpretation of regexp special
characters and requiring an anchored, entire match.

Pattern expressions following the command verb may not contain literal
whitespace; use \s or \t if you need to. Event-selection
regexps may contain literal whitespace.

Regular expressions are not anchored.  Use ^ and $ to anchor them
to the beginning or end of the search space, when appropriate.

Some commands support regular expression flags, and some even add
additional flags over the standard set. The documentation for each
individual command will include these details.
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

----
`)
	for _, item := range verbosityLevelList() {
		fmt.Println(item.k)
	}
	fmt.Println("----")
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
func (rs *Reposurgeon) DoLog(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
	line = strings.Replace(line, ",", " ", -1)
	for _, tok := range strings.Fields(line) {
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
	if len(line) == 0 || control.isInteractive() {
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

Error, warning, and diagnostic messages are normally emitted to standard
error.  This command, with a nonempty _path_ argument, directs them to
the specified file instead.  Without an argument, reports what logfile is
set.
`)
}

// DoLogfile is the handler for the "logfile" command.
func (rs *Reposurgeon) DoLogfile(line string) bool {
	if len(line) != 0 {
		fp, err := os.OpenFile(line, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, userReadWriteMode)
		if err != nil {
			respond("log file open failed: %v", err)
		} else {
			var i interface{} = fp
			control.logfp = i.(io.Writer)
		}
	}
	if len(line) == 0 || control.isInteractive() {
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
tests.
`)
}

// DoPrint is the handler for the "print" command.
func (rs *Reposurgeon) DoPrint(line string) bool {
	parse := rs.newLineParse(line, parseNOSELECT, []string{"stdout"})
	defer parse.Closem()
	fmt.Fprintf(parse.stdout, "%s\n", parse.line)
	return false
}

// HelpScript says "Shut up, golint!"
func (rs *Reposurgeon) HelpScript() {
	rs.helpOutput(`
script PATH [ARG...]

Takes a filename and optional following arguments.
Reads each line from the file and executes it as a command.

During execution of the script, the script name replaces the
string "$0", and the optional following arguments (if any) replace the
strings "$1", "$2" ... "$n" in the script text. This is done before
tokenization, so the "$1" in a string like "foo$1bar" will
be expanded.  Additionally, "$$" is expanded to the current process ID
(which may be useful for scripts that use tempfiles).

Within scripts (and only within scripts) reposurgeon accepts a
slightly extended syntax: First, a backslash ending a line signals
that the command continues on the next line. Any number of consecutive
lines thus escaped are concatenated, without the ending backslashes,
prior to evaluation. Second, a command that takes an input filename
argument can instead take literal data using the syntax of a shell
here-document. That is: if the "<filename" is replaced by
"<<EOF", all following lines in the script up to a terminating
line consisting only of "EOF" will be read, placed in a
temporary file, and that file fed to the command and afterwards
deleted.  "EOF" may be replaced by any string. Backslashes have no
special meaning while reading a here-document.

Scripts may have comments.  Any line beginning with a "#" is
ignored. If a line has a trailing portion that begins with one or more
whitespace characters followed by "#", that trailing portion is
ignored.

Scripts may call other scripts to arbitrary depth.

When running a cript interacttively, you can abort it by typing Ctl-C 
abnd return to the top-level prompt. The abort flag is checked after each
script line is executed. 
`)
}

// DoScript is the handler for the "script" command.
func (rs *Reposurgeon) DoScript(ctx context.Context, line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
	interpreter := rs.cmd
	if len(line) == 0 {
		respond("script requires a file argument\n")
		return false
	}
	words := strings.Split(line, " ")
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
[SELECTION] hash [--tree] [>OUTFILE]

Report Git event hashes.  This command simulates Git hash generation.

Takes a selection set, defaulting to all.  For each eligible event in the set,
returns its index and the same hash that Git would generate for its
representation of the event. Eligible events are blobs and commits.

With the option --bare, omit the event number; list only the hash.

With the option --tree, generate a tree hash for the specified commit rather
than the commit hash. This option is not expected to be useful for anything
but verifying the hash code itself.
`)
}

// DoHash is the handler for the "hash" command.
func (rs *Reposurgeon) DoHash(line string) bool {
	parse := rs.newLineParse(line, parseALLREPO, orderedStringSet{"stdout"})
	defer parse.Closem()
	repo := rs.chosen()
	for _, eventid := range rs.selection {
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

// HelpSizeof says "Shut up, golint!"
func (rs *Reposurgeon) HelpSizeof() {
	rs.helpOutput(`
sizeof

This command is for developer use when optimizing structure packing to reduce
memory use. It is probably not of interest to ordinary reposurgeon users.

It displays byte-extent sizes for various reposurgeon internal types.  Note
that these sizes are stride lengths, as in C's sizeof(); this means that for
structs they will include whatever trailing padding is required for instances
in an array of the structs.
`)
}

// DoSizeof is for developer use when optimizing structure packing to reduce memory use
// const MaxUint = ^uint(0)
// const MinUint = 0
// const MaxInt = int(MaxUint >> 1)
// const MinInt = -MaxInt - 1
func (rs *Reposurgeon) DoSizeof(line string) bool {
	rs.newLineParse(line, parseNOSELECT, nil)
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
	fmt.Fprintf(control.baton, "map[string]bool:   %3d\n", unsafe.Sizeof(make(map[string]bool)))
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
				stop1 := interpreter.OneCmd(ctx, acmd)
				stop2 := interpreter.PostCmd(ctx, stop, acmd)
				if stop1 || stop2 {
					goto breakout
				}
			}
		}
	}
breakout:
	interpreter.PostLoop(ctx)
	r.End()
	// Fall through to defer hook.
}

// end
