// vcs - encapsulates of VCS capabilities

// SPDX-FileCopyrightText: Eric S. Raymond <esr@thyrsus.com>
// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"regexp"
	"strings"
)

// Most knowledge about specific version-control systems lives in the
// following class list. But svnread.go is a large exception: smaller ones
// elewhere in the code are marked "BEWARE, ADHESION".
//
// Import/export style flags are as follows:
//     "no-nl-after-commit" = no extra NL after each commit
//     "nl-after-comment" = inserts an extra NL after each comment
//     "export-progress" = exporter generates its own progress messages,
//                         no need for baton prompt.
//     "import-defaults" = Import sets default ignores
//
// Preserve and prenuke parts can be directories.
//
// Note that some of the commands used here are plugins or extensions
// that are not part of the basic VCS. Thus these may fail when called;
// we need to be prepared to cope with that.
//
// ${pwd} is replaced with the name of the present working directory.

// VCS is a class representing a version-control system.
type VCS struct {
	name         string           // Name of the VCS
	subdirectory string           // Name of its metadata subdirectory
	exporter     string           // Import/export style flags.
	requires     stringSet        // Required tools
	quieter      string           // How to make exporter quieter
	styleflags   orderedStringSet // fast-export style flags
	extensions   orderedStringSet // Format extension flags
	initializer  string           // Command to initualize a repo
	pathlister   string           // Command to list registered files
	taglister    string           // Command to list tag names
	branchlister string           // Command to list branch names
	importer     string           // Command to import from stream format
	checkout     string           // Command to check out working copy
	viewer       string           // GUI command to browse with
	preserve     orderedStringSet // Config and hook stuff to be preserved
	prenuke      orderedStringSet // Things to be removed from staging
	authormap    string           // Where importer might drop an authormap
	ignorename   string           // Where the ignore patterns live
	project      string           // VCS project URL
	notes        string           // Notes and caveats
	// Hidden members
	cookies     []regexp.Regexp // How to recognize a possible commit reference
	checkignore string          // how to tell if directory is a checkout
	idformat    string          // ID display string format
	flags       uint            // Capability flags
	// One last visible member
	dfltignores string // Default ignore patterns
}

// Capability flags for grokking ignore file syntax.
//
// This is a complicated and murky area because VCSs are very bad
// at documenting their actual rules. Here are some references:
//
// bk: https://www.bitkeeper.org/man/ignore.html
// bzr/brz: https://documentation.help/Bazaar-help/controlling_registration.html
// cvs: https://www.gnu.org/software/trans-coord/manual/cvs/html_node/cvsignore.html
// darcs: https://darcs.net/Using/Configuration#boring
// fossil: https://fossil-scm.org/home/doc/trunk/www/globs.md
// git: https://git-scm.com/docs/gitignore
// hg: https://www.selenic.com/mercurial/hgignore.5.html
// mtn: https://www.monotone.ca/docs/Regexps.html
// p4: https://www.perforce.com/manuals/v15.2/cmdref/P4IGNORE.html
// svn: https://svnbook.red-bean.com/nightly/en/svn.advanced.props.special.ignore.html
//
// SCCS and RCS don't have a native ignore facility.
//
// POSIX fnmatch(3): https://pubs.opengroup.org/onlinepubs/9699919799/functions/fnmatch.html
// POSIX glob(3): https://pubs.opengroup.org/onlinepubs/9699919799/functions/glob.html
// Python glob(3): https://docs.python.org/3/library/glob.html
//
// There are two different kinds of ignore-pattern syntax. Most VCSes
// use some variation on glob(3)/fnmatch(3); glob(3) is like
// fnmatch(3) with FNM_NOESCAPE unset but FNM_PATHNAME and FNM_PERIOD
// set. Some VCSes (darcs, mtn) use full regular expressions. One (hg)
// defaults to globbing but can use either.
//
// Predicting features from knowing which library is used isn't
// simple, because POSIX glob(3) optionally has features that
// original shell globbing did not, including dash ranges,
//
// Just to make things more confusing, there are different versions of
// the fnmatch library, not all of them have the same features, and
// not all document everything they support. CVS uses a local port of
// a very old version. The Python fnmatch library doesn't document its
// support for dash ranges.
//
// There are some complications around / relating to which of
// the following rules is applied:
//
// A. Matches apply to subdirectories - ignLOOSE.
// B. Matches are anchored to the directory where the ignore
//    file is - ~ignLOOSE.
//
// The presence of a / in a path may change whether A or B applies.
//
// Glob behavior of most of these (brz, bzr, fossil, git, hg, src, svn) are
// verified by our test suite. cvs behavior is checked by code
// inspection. bk, darcs, and p4 are checked against their documentation.
//
//           Specials  FNMPATH   NEG     LOOSE    FNMDOT   DSTAR    ASLASH   DIRMATCH
//           --------  -------   ------  -------  -------  -------  -------  -------
// bk        *         ?         no      yes      ?        no       yes      ?
// bzr/brz:  *?[^!-]   no        yes     yes      no       yes      no       no
// cvs:      *?[^!-]\  no        no      no       no       no       no       no
// darcs:    [^-]\     no        no      yes      no       no       no       no
// fossil:   *?[^-]\   no        no      no       no       yes      no       yes
// git:      *?[^!-]\  yes       yes     yes      no       yes      yes      yes
// hg:       *[^-]\    yes       no      yes      no       no       no       yes
// p4:       *         yes       yes     yes      ?        yes      yes      yes
// src:      *?[^!-]\  yes       yes     no       yes      no       yes      no
// svn:      *?[^!-]\  yes       yes     no       no       no       yes      no
//
// All these systems have #-led comments. CVS doesn't, but the only
// CVS ignore patterns we'll ever see are in .cvsignore and .gitignore
// files imported with that feature.
//
// bzr/brz allows only one ignore file, at the repository root.
// There's a unique !!  syntax "Patterns prefixed with '!!' act as
// regular ignore patterns, but have highest precedence, even over the
// '!'  exception patterns.". An RE: prefix on a pattern line means it
// should be interpreted as a regular expression.
//
// cvs uses a local workalike of fnmatch(3).  The FNM_PATHNAME,
// FNM_NOESCAPE, and FNM_PERIOD flags are *not* set.  A line consisting of
// a single ! clears all ignore patterns. "The patterns found in
// .cvsignore are only valid for the directory that contains them, not
// for any sub-directories."
//
// darcs and mtn use full regexps rather than any version of
// fnmatch(3)/glob(3).  darcs ignore capabilities have to be inferred
// from the documentation, because there is no variant of git status
// for which they affect the output.
//
// fossil explicitly documents that ? and * can match /. It has two
// different mechanisms for ignore patters: the ignore-glob setting
// through the Web interface or CLI, and local (versioned) settings in
// a dotfile.  The unversioned ignore-glob setting isn't supported
// because fossil fast-export doesn't ship it in the output stream.
// We describe its capabilities for completeness.  "The glob must
// match the entire canonical file name to be considered a match."
//
// git does an equivalent of fnmatch(3) with FNM_PATHNAME on,
// FNM_NOESCAPE. and FNM_PERIOD off. ignLOOSE applies unless there's
// an initial or nedial separator, in which case rule B. A / at end of
// pattern has the special behavior of matching only directories.
//
// hg uses globbing or regexps depending on whether "syntax: regexp\n"
// or "syntax: glob\n" has been seen most recently. The default is
// globs (tested).
//
// mtn has been moribund since 2011, isn't packaged for Linux, and is
// probably no longer in live use anywhere; the effort required to
// test it can't really be justified it until somebody files an
// issue.
//
// src uses Python's glob library and inherits those behaviors. It
// adds support for prefix negation with ! and for ^ as a range
// negator.
//
// svn documents that it uses glob(3) and says "if you are migrating a
// CVS working copy to Subversion, you can directly migrate the ignore
// patterns by using the .cvsignore file as input to the svn propset
// command."; however this is not true as the implied settings of
// FNM_PATHNAME differs between glob(3) and CVS.  svn:global-ignore
// properties (introduced in Subversion 1.8) set in the repository
// root apply to subdirectories; svn:ignore properties do not. Just to
// complicate matters, 1.8 and later have svn:global-ignores defaults
// identical to the previous global-ignores defaults...and "The ignore
// patterns in the svn:global-ignores property may be delimited with
// any whitespace (similar to the global-ignores runtime configuration
// option), not just newlines (as with the svn:ignore property)."!
// Also: "Once an object is under Subversion's control, the ignore
// pattern mechanisms no longer apply to it."
//
// bk doesn't document its ignore syntax at all and the examples only
// show *. Since we never expect to export *to* bk, we'll make the
// conservative assunmption that supports only old-fashioned shell
// globbing. "Patterns that do not contain a slash (`/') character are
// matched against the basename of the file; patterns containing a
// slash are matched against the pathname of the file relative to the
// root of the repository.  Using './' at the start of a pattern means
// the pattern applies only to the repository root."  Rule A, with the
// ignASLASH feature.
//
// There's no direct p4 support yet. What's recorded here anticipates
// that that might someday change. There's a supplement to the p4 docs at
// https://stackoverflow.com/questions/18240084/how-does-perforce-ignore-file-syntax-differ-from-gitignore-syntax
//
// Yes, the capability flags defined below aren't all used. Yet.

const (
	ignASLASH     uint = 1 << iota // A / changes matching from LOOSE to anchored
	ignBANG                        // Negate rangest with !
	ignBZR                         // bzr or its clone, brz; RE: syntax
	ignCARET                       // Negate rangest with !
	ignDIRMATCH                    // Terminal slash matches directories
	ignDSTAR                       // Match multiple path segments
	ignESC                         // Backslash escape glob characters
	ignEXPORT                      // Ignore patterns are visible via fast-export only
	ignFNMDOT                      // Leading period requires explicit match (POSIX FNM_PERIOD)
	ignFNMPATH                     // Glob wildcards can't match / (POSIX FNM_PATHNAME)
	ignGLOB                        // Basic globbing: *[-]
	ignHASH                        // Has native ignorefile comments led by hash
	ignLOOSE                       // Ignore patterns apply to subdirectories
	ignNEG                         // Ignore patterns allow prefix negation with !
	ignQUES                        // Allow ? to match any character
	ignRE                          // Patterns are full regular expressions
	ignWACKYSPACE                  // Spaces are treated as pattern separators
)

// These capabilities come with GNU fnmatch(3)
const ignFNMATCH = ignESC | ignGLOB | ignQUES | ignBANG | ignCARET | ignFNMPATH

// Constants needed in VCS class methods.
//
// These are for detecting things that look like revision references.
// They look a little strange on the end because we wannt to be able
// to detect them eitrher surrounded by whitespace or at the end of a
// sentence.
const tokenNumeric = `\s[0-9]+(\s|[.][^0-9])`
const dottedNumeric = `\s[0-9]+(\.[0-9]+[.]?)+\s`
const shortGitHash = `\b[0-9a-fA-F]{6}[^0-9a-zA-z]`
const longGitHash = `\b[0-9a-fA-F]{40}[^0-9a-zA-z]`

// manages tells us if a directory might be managed by this VCS
func (vcs VCS) manages(dirname string) bool {
	if vcs.subdirectory != "" {
		subdir := filepath.Join(dirname, vcs.subdirectory)
		subdir = filepath.FromSlash(subdir)
		if exists(subdir) && isdir(subdir) {
			return true
		}
	}
	// Could be a CVS repository without CVSROOT
	if vcs.name == "cvs" {
		files, err := ioutil.ReadDir(dirname)
		if err == nil {
			for _, p := range files {
				if strings.HasSuffix(p.Name(), ",v") {
					return true
				}
			}
		}
	}
	// Could be a Fossil repository, look for checkout's state file.
	if vcs.name == "fossil" && isdir(".fslckout") {
		return true
	}
	return false
}

func (vcs VCS) String() string {
	realignores := newOrderedStringSet()
	scanner := bufio.NewScanner(strings.NewReader(vcs.dfltignores))
	for scanner.Scan() {
		item := scanner.Text()
		if len(item) > 0 && !strings.HasPrefix(item, "# ") {
			realignores.Add(item)
		}
	}
	notes := strings.Trim(vcs.notes, "\t ")

	return fmt.Sprintf("         Name: %s\n", vcs.name) +
		fmt.Sprintf(" Subdirectory: %s\n", vcs.subdirectory) +
		fmt.Sprintf("     Requires: %s\n", vcs.requires.String()) +
		fmt.Sprintf("     Exporter: %s\n", vcs.exporter) +
		fmt.Sprintf(" Export-Style: %s\n", vcs.styleflags.String()) +
		fmt.Sprintf("   Extensions: %s\n", vcs.extensions.String()) +
		fmt.Sprintf("  Initializer: %s\n", vcs.initializer) +
		fmt.Sprintf("   Pathlister: %s\n", vcs.pathlister) +
		fmt.Sprintf("    Taglister: %s\n", vcs.taglister) +
		fmt.Sprintf(" Branchlister: %s\n", vcs.branchlister) +
		fmt.Sprintf("     Importer: %s\n", vcs.importer) +
		fmt.Sprintf("     Checkout: %s\n", vcs.checkout) +
		fmt.Sprintf("       Viewer: %s\n", vcs.viewer) +
		fmt.Sprintf("      Prenuke: %s\n", vcs.prenuke.String()) +
		fmt.Sprintf("     Preserve: %s\n", vcs.preserve.String()) +
		fmt.Sprintf("    Authormap: %s\n", vcs.authormap) +
		fmt.Sprintf("   Ignorename: %s\n", vcs.ignorename) +
		fmt.Sprintf("      Project: %s\n", vcs.project) +
		fmt.Sprintf("        Notes: %s\n", notes) +
		fmt.Sprintf("      Ignores: %s\n", realignores.String())
}

// Used for pre-compiling regular expressions at module load time
func reMake(patterns ...string) []regexp.Regexp {
	regexps := make([]regexp.Regexp, 0)
	for _, item := range patterns {
		regexps = append(regexps, *regexp.MustCompile(item))
	}
	return regexps
}

func (vcs VCS) hasReference(comment []byte) bool {
	for i := range vcs.cookies {
		if vcs.cookies[i].Find(comment) != nil {
			return true
		}
	}
	return false
}

var vcstypes []VCS
var ignoremap map[string]*VCS

func vcsInit() {
	vcstypes = []VCS{
		{
			name:         "git",
			subdirectory: ".git",
			// Requires git 2.19.2 or later for --show-original-ids
			requires:     newStringSet("git", "cut", "grep"),
			exporter:     "git fast-export --show-original-ids --signed-tags=verbatim --tag-of-filtered-object=drop --use-done-feature --all",
			quieter:      "",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "git init --quiet",
			pathlister:   "git ls-files",
			taglister:    "git tag -l",
			branchlister: "git branch -q --list 2>&1 | cut -c 3- | grep -E -v 'detached|^master$' || exit 0",
			importer:     "git fast-import --quiet --export-marks=.git/marks",
			checkout:     "git checkout",
			viewer:       "gitk --all",
			prenuke:      newOrderedStringSet(".git/config", ".git/hooks"),
			preserve:     newOrderedStringSet(".git/config", ".git/hooks"),
			authormap:    ".git/cvs-authors",
			ignorename:   ".gitignore",
			cookies:      reMake(shortGitHash, longGitHash),
			project:      "http://git-scm.com/",
			notes:        "The authormap is not required, but will be used if present.",
			idformat:     "%s",
			flags:        ignHASH | ignFNMATCH | ignNEG | ignDSTAR | ignLOOSE | ignASLASH | ignDIRMATCH,
			dfltignores:  "",
		},
		{
			name:         "bzr",
			subdirectory: ".bzr",
			requires:     newStringSet("bzr", "cut"),
			exporter:     "bzr fast-export --no-plain .",
			quieter:      "",
			styleflags: newOrderedStringSet(
				"export-progress",
				"no-nl-after-commit",
				"nl-after-comment"),
			extensions: newOrderedStringSet(
				"empty-directories",
				"multiple-authors", "commit-properties"),
			initializer:  "bzr init --quiet",
			pathlister:   "bzr ls",
			taglister:    "bzr tags",
			branchlister: "bzr branches | cut -c 3-",
			importer:     "bzr fast-import -",
			checkout:     "bzr checkout",
			viewer:       "bzr qlog",
			prenuke:      newOrderedStringSet(".bzr/plugins"),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   ".bzrignore",
			cookies:      reMake(tokenNumeric),
			project:      "http://bazaar.canonical.com/en/",
			notes:        "Requires the bzr-fast-import plugin.",
			idformat:     "%s",
			flags:        ignHASH | ignGLOB | ignQUES | ignBANG | ignNEG | ignLOOSE | ignBZR | ignDSTAR | ignASLASH,
			dfltignores: `# A simulation of bzr default ignores, generated by reposurgeon.
*.a
*.o
*.py[co]
*.so
*.sw[nop]
*~
.#*
[#]*#
__pycache__
bzr-orphans
# Simulated bzr default ignores end here
`,
		},
		{
			name:         "brz",
			subdirectory: ".brz",
			requires:     newStringSet("brz", "cut"),
			exporter:     "brz fast-export --no-plain .",
			quieter:      "",
			styleflags: newOrderedStringSet(
				"export-progress",
				"no-nl-after-commit",
				"nl-after-comment"),
			extensions: newOrderedStringSet(
				"empty-directories",
				"multiple-authors", "commit-properties"),
			initializer:  "brz init --quiet",
			pathlister:   "brz ls",
			taglister:    "brz tags",
			branchlister: "brz branches | cut -c 3-",
			importer:     "brz fast-import -",
			checkout:     "brz checkout",
			viewer:       "brz qlog",
			prenuke:      newOrderedStringSet(".brz/plugins"),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			project:      "https://www.breezy-vcs.org/",
			ignorename:   ".bzrignore", // This is not a typo. It *isn't* .brzignore
			cookies:      reMake(tokenNumeric),
			notes:        "Breezy capability is not well tested.",
			idformat:     "%s",
			flags:        ignHASH | ignGLOB | ignQUES | ignNEG | ignLOOSE | ignBZR | ignDSTAR | ignASLASH,
			dfltignores: `# A simulation of brz default ignores, generated by reposurgeon.
 *.a
 *.o
 *.py[co]
 *.so
 *.sw[nop]
 *~
 .#*
 [#]*#
 __pycache__
 brz-orphans
 # Simulated brz default ignores end here
 `,
		},
		{
			name:         "hg",
			subdirectory: ".hg",
			requires:     newStringSet("hg"),
			exporter:     "",
			styleflags: newOrderedStringSet(
				"import-defaults",
				"nl-after-comment",
				"export-progress"),
			extensions:   newOrderedStringSet(),
			initializer:  "hg init --quiet",
			pathlister:   "hg status -macn",
			taglister:    "hg tags --quiet",
			branchlister: "hg branches --closed --template '{branch}\n' | grep -v '^default$'",
			importer:     "hg-git-fast-import .",
			checkout:     "hg checkout",
			viewer:       "hgk",
			prenuke:      newOrderedStringSet(".hg/hgrc"),
			preserve:     newOrderedStringSet(".hg/hgrc"),
			authormap:    "",
			ignorename:   ".hgignore",
			cookies:      reMake(`\b[0-9a-f]{40}\b`, `\b[0-9a-f]{12}\b`),
			project:      "https://www.mercurial-scm.org/",
			notes: `If there is no branch named 'master' in a repo when it is read, the hg 'default'
branch is renamed to 'master'.
`,
			idformat:    "%s",
			flags:       ignHASH | ignGLOB | ignESC | ignCARET | ignLOOSE | ignFNMPATH,
			dfltignores: "",
		},
		{
			// Styleflags may need tweaking for round-tripping
			name:         "darcs",
			subdirectory: "_darcs",
			requires:     newStringSet("darcs"),
			exporter:     "darcs convert export 2>/dev/null",
			quieter:      "",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "darcs initialize",
			pathlister:   "darcs show files",
			taglister:    "darcs show tags",
			branchlister: "",
			importer:     "darcs convert import --quiet >/dev/null",
			checkout:     "",
			viewer:       "",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   "_darcs/prefs/boring",
			cookies:      reMake(),
			project:      "http://darcs.net/",
			notes:        "Assumes no boringfile preference has been set.",
			idformat:     "%s",
			flags:        ignRE,
			// darcs doesn't have wired defaults. Instead there is a nonempty
			// default ignore-pattern file which we'll rename when required.
			dfltignores: ``,
		},
		/*
			{
				name:         "pijul",
				subdirectory: ".pijul",
				requires:     newStringSet("pijul", "cut"),
				exporter:     "",
				quieter:      "",
				styleflags:   newOrderedStringSet(),
				extensions:   newOrderedStringSet(),
				initializer:  "pijul init",
				pathlister:   "pijul ls", // Undocumented
				taglister:    "",
				branchlister: "pijul channels 2>&1 | cut -c 3-",
				importer:     "",
				checkout:     "",
				viewer:      "",
				prenuke:      newOrderedStringSet(),
				preserve:     newOrderedStringSet(),
				authormap:    "",
				ignorename:   ".ignore",
				cookies:      reMake(),
				project:      "http://pijul.org/",
				notes:        "No importer/exporter pair yet.",
				idformat:     "%s",
				flags:        ignRE,
				dfltignores:  ``,
			},
		*/
		{
			name:         "mtn",
			subdirectory: "_MTN",
			requires:     newStringSet("mtn"),
			exporter:     "mtn git_export",
			quieter:      "",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "", // No single command does this due to wacky db setup
			pathlister:   "mtn list known",
			taglister:    "mtn list tags",
			branchlister: "mtn list branches",
			importer:     "",
			checkout:     "",
			viewer:       "",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   ".mtn_ignore", // Assumes default hooks
			cookies:      reMake(),
			project:      "http://www.monotone.ca/",
			notes:        "Exporter is buggy, occasionally emitting negative timestamps.",
			idformat:     "%s",
			flags:        ignRE,
			dfltignores: `# A simulation of mtn default ignores, generated by reposurgeon.
*.a
*.so
*.o
*.la
*.lo
^core
*.class
*.pyc
*.pyo
*.g?mo
*.intltool*-merge*-cache
*.aux
*.bak
*.orig
*.rej
%~
*.[^/]**.swp
*#[^/]*%#
*.scc
^*.DS_Store
/*.DS_Store
^desktop*.ini
/desktop*.ini
autom4te*.cache
*.deps
*.libs
*.consign
*.sconsign
CVS
*.svn
SCCS
_darcs
*.cdv
*.git
*.bzr
*.hg
# Simulated mtn default ignores end here
`,
		},
		{
			name:         "svn",
			subdirectory: "locks",
			requires:     newStringSet("svn", "sed"),
			exporter:     "svnadmin dump  .",
			quieter:      "--quiet",
			styleflags:   newOrderedStringSet("import-defaults", "export-progress"),
			extensions:   newOrderedStringSet(),
			initializer:  "svnadmin create .",
			importer:     "",
			pathlister:   "svn ls",
			taglister:    "svn ls 'file://${pwd}/tags' | sed 's|/$||'",
			branchlister: "svn ls 'file://${pwd}/branches' | sed 's|/$||'",
			checkout:     "",
			viewer:       "",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet("hooks"),
			authormap:    "",
			ignorename:   "",
			cookies:      reMake(`\sr?\d+[.;]?\s`),
			project:      "http://subversion.apache.org/",
			notes:        "Run from the repository, not a checkout directory.",
			checkignore:  ".svn",
			idformat:     "r%s",
			flags:        ignEXPORT | ignFNMATCH | ignNEG | ignASLASH,
			// These defaults are unanchored, which strictly speaking is correct only for svn 1.8
			// and later where they are set as global-ignore rather than ignore properties.
			dfltignores: `# A simulation of Subversion default ignores, generated by reposurgeon.
*.o
*.lo
*.la
*.al
*.libs
*.so
*.so.[0-9]*
*.a
*.pyc
*.pyo
*.rej
*~
*.#*
.*.swp
.DS_store
# Simulated Subversion default ignores end here
`,
		},
		{
			name:         "cvs",
			subdirectory: "CVSROOT", // Can't be Attic, that doesn't always exist.
			requires:     newStringSet("cvs-fast-export", "find", "grep", "awk"),
			exporter:     "find . -print | cvs-fast-export --reposurgeon",
			quieter:      "-q",
			styleflags:   newOrderedStringSet("import-defaults", "export-progress"),
			extensions:   newOrderedStringSet(),
			initializer:  "cvs init",
			importer:     "",
			checkout:     "",
			viewer:       "",
			pathlister:   "",
			// CVS code will screw up if any tag is not common to all files
			// Hacks at https://stackoverflow.com/questions/6174742/how-to-get-a-list-of-tags-created-in-cvs-repository
			// would be better (fewer dependencies) but they seem to be for running in a checkout directory.
			taglister:    "module=`ls -1 | grep -v CVSROOT`; cvs -Q -d:local:${pwd} rlog -h $module 2>&1 | awk -F'[.:]' '/^\t/&&$(NF-1)!=0{print $1}' |awk '{print $1}' | sort -u",
			branchlister: "module=`ls -1 | grep -v CVSROOT`; cvs -Q -d:local:${pwd} rlog -h $module 2>&1 | awk -F'[.:]' '/^\t/&&$(NF-1)==0{print $1}' |awk '{print $1}' | sort -u",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   "",
			cookies:      reMake(dottedNumeric),
			project:      "http://www.catb.org/~esr/cvs-fast-export",
			notes:        "Requires cvs-fast-export.",
			checkignore:  "CVS",
			idformat:     "%s",
			flags:        ignEXPORT | ignFNMATCH | ignWACKYSPACE,
			// "\#*" is escaped because, while natively CVS
			// doesn't have # comments, these defaults are
			// in git format.  Also, WACKYSPACE is only set for
			// documentation purposes; cvs-fast-export
			// will have changed those into newlines.
			dfltignores: `
# A simulation of cvs default ignores, generated by reposurgeon.
tags
TAGS
.make.state
.nse_depinfo
*~
\#*
.#*
,*
_$*
*$
*.old
*.bak
*.BAK
*.orig
*.rej
.del-*
*.a
*.olb
*.o
*.obj
*.so
*.exe
*.Z
*.elc
*.ln
core
# Simulated cvs default ignores end here
`,
		},
		{
			name:         "sccs",
			subdirectory: "SCCS",
			requires:     newStringSet("sccs", "src"),
			exporter:     "src sccs fast-export --reposurgeon",
			quieter:      "",
			styleflags:   newOrderedStringSet("export-progress"),
			extensions:   newOrderedStringSet(),
			initializer:  "mkdir SCCS",
			pathlister:   "src sccs ls",
			taglister:    "src sccs tag list",
			branchlister: "src sccs branch list",
			importer:     "",
			checkout:     "",
			viewer:       "",
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   "",
			dfltignores:  "", // Has none
			cookies:      reMake(dottedNumeric),
			project:      "https://www.gnu.org/software/cssc/",
			notes:        "",
			idformat:     "%s",
			flags:        ignEXPORT | ignNEG | ignFNMATCH | ignFNMDOT | ignASLASH, // Through src
		},
		{
			name:         "rcs",
			subdirectory: "RCS",
			requires:     newStringSet("rcs", "src"),
			exporter:     "src rcs fast-export --reposurgeon",
			quieter:      "",
			styleflags:   newOrderedStringSet("export-progress"),
			extensions:   newOrderedStringSet(),
			initializer:  "mkdir RCS",
			pathlister:   "src rcs ls",
			taglister:    "src rcs tag list",
			branchlister: "src rcs branch list",
			importer:     "",
			checkout:     "",
			viewer:       "",
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   "",
			dfltignores:  "", // Has none
			cookies:      reMake(dottedNumeric),
			project:      "https://www.gnu.org/software/rcs/",
			notes:        "",
			idformat:     "%s",
			flags:        ignEXPORT | ignNEG | ignFNMATCH | ignFNMDOT | ignASLASH, // Through src
		},
		{
			name:         "src",
			subdirectory: ".src",
			requires:     newStringSet("src", "rcs"),
			exporter:     "src fast-export --reposurgeon",
			quieter:      "",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "mkdir .src",
			pathlister:   "src ls",
			taglister:    "src tag list",
			branchlister: "src branch list",
			importer:     "",
			checkout:     "",
			viewer:       "",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   ".srcignore",
			dfltignores:  "", // Has none
			cookies:      reMake(tokenNumeric),
			project:      "http://catb.org/~esr/src",
			notes:        "",
			idformat:     "%s",
			flags:        ignHASH | ignNEG | ignFNMATCH | ignFNMDOT | ignASLASH,
		},
		{
			// Styleflags may need tweaking for round-tripping
			name:         "bk",
			subdirectory: ".bk",
			requires:     newStringSet("bk", "sed"),
			exporter:     "bk fast-export --no-bk-keys",
			quieter:      "-q",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "", // bk setup doesn't work here
			pathlister:   "bk gfiles -U",
			taglister:    "bk tags | sed -n 's/ *TAG: *//p'",
			branchlister: "",
			importer:     "bk fast-import -q",
			checkout:     "",
			viewer:       "bk viewer",
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   "BitKeeper/etc/ignore",
			dfltignores:  "",                    // Has none
			cookies:      reMake(dottedNumeric), // Same as SCCS/CVS
			project:      "https://www.bitkeeper.com/",
			notes:        "Bitkeeper's importer is flaky and incomplete as of 7.3.1ce.",
			idformat:     "%s",
			flags:        ignGLOB | ignLOOSE | ignASLASH,
		},
		{
			// Styleflags may need tweaking for round-tripping
			name:         "fossil",
			subdirectory: "", // There's a special case in manages()
			requires:     newStringSet("fossil"),
			exporter:     "fossil export --git",
			quieter:      "",
			styleflags:   newOrderedStringSet(),
			extensions:   newOrderedStringSet(),
			initializer:  "fossil init .fossil && fossil open .fossil",
			pathlister:   "", // fossil extras is the inverse of this
			taglister:    "fossil tag list",
			branchlister: "fossil branch list", // Should we list with --all? Unclear...
			importer:     "fossil import --git",
			checkout:     "",
			viewer:       "", // fossil ui looks tempting but has no clean exit.
			prenuke:      newOrderedStringSet(),
			preserve:     newOrderedStringSet(),
			authormap:    "",
			ignorename:   ".fossil-settings/ignore-glob",
			dfltignores:  "", // ignore-glob is empty by default
			cookies:      nil,
			project:      "https://fossil-scm.org/",
			notes:        "",
			idformat:     "%s",
			flags:        ignGLOB | ignQUES | ignCARET | ignESC | ignDSTAR,
		},
	}

	// We'll use this to deduce the types of streams that contain ignore files.
	ignoremap = make(map[string]*VCS)
	for i, vcs := range vcstypes {
		if vcs.ignorename != "" {
			ignoremap[vcs.ignorename] = &vcstypes[i]
		}
	}
}

// findVCS finds a VCS by name
func findVCS(name string) *VCS {
	for _, vcs := range vcstypes {
		if vcs.name == name {
			return &vcs
		}
	}
	panic(fmt.Sprintf("reposurgeon: failed to find '%s' in VCS types (len %d)", name, len(vcstypes)))
}

// identifyRepo finds what type of repo we're looking at.
func identifyRepo(dirname string) *VCS {
	for _, vcs := range vcstypes {
		if vcs.manages(dirname) {
			return &vcs
		}
	}
	return nil
}

func (vcs VCS) hasCapability(n uint) bool {
	return (n & vcs.flags) != 0
}

// end
