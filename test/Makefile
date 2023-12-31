# Test-suite makefile for reposurgeon

# Different implementations could be plugged in to the tests.
REPOSURGEON := reposurgeon 
REPOCUTTER := repocutter 
REPOTOOL := repotool
REPOMAPPER := repomapper

# Setting this to 1 suppresses detail sections in most TAP reports.
QUIET=0

# The TAP filter. Only affects presentation of the test suite messages
TAPCONSUMER=tapview

# No user-serviceable parts below this line.

# Use absolute path so tests that change working directory still use 
# scripts from parent directory.  Note that using $PWD seems to fail
# here under Gitlab's CI environment.
PATH := $(BINDIR):$(realpath ..):$(realpath .):${PATH}

# Force the timezone in case CI has a different idea.
# Havoc ensues if this is not exported - not clear why.
export TZ=UTC

# Force pure serial execution when rebuilding check files.  Slower,
# but makes them deterministic and may help smoke out bugs in
# concurrent code.
BUILDOPT="set flag serial"

# See https://stackoverflow.com/questions/6481005/how-to-obtain-the-number-of-cpus-cores-in-linux-from-the-command-line
#CONCURRENT_JOBS=$(shell getconf _NPROCESSORS_ONLN || getconf NPROCESSORS_ONLN || echo 4)

# Fall back to safety if our declared TAP consumer does not exist.
# This is helpful in the CI environment, where it wiuld be better for
# the logfiles to carry the raw TAP messages. 
TAPFILTER=$(shell command -v $(TAPCONSUMER) || echo cat)

# Run specified tests in parallel and ship their output to our TAP consumer after a plan header
TAPOUT = (echo "1..$(words $(1))"; $(MAKE) --output-sync --no-print-directory -j $(CONCURRENT_JOBS) $(1)) | $(TAPFILTER)

# Test whether we have a specified command
AVAILABLE=command -v $(1) >/dev/null 2>&1

# Parallel make doesn't return a fail status when a single subprocess fails,
# so we need to log the failures and check the log for nonemptiness in order
# to hand a real status back. This is important for CI/CD.  
FAILLOG=/tmp/reposurgeon-make.log

# The default, run all tests.
# parallel-tap must not itself be run in parallel.
check:
	@$(MAKE) -j1 --no-print-directory parallel-tap

.SUFFIXES: .svn .chk .fi .map .sh .see

.svn.chk:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@ 2>&1
.svn.fi:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@
.svn.map:
	$(REPOSURGEON) "log -all" "read <$<" "legacy write -" >$@
.sh.svn:
	sh $< -o $@
.sh.fi:
	sh $< -o $@
.svn.see:
	@repocutter -q see <$< >$@

clean:
	@rm -fr .rs* test=repo test-checkout test-repo* test-checkout* git-repo left-repo right-repo *~

options:
	@echo "REPOSURGEON is '$(REPOSURGEON)'"
	@echo "TESTOPT is '$(TESTOPT)'"

# Show summary lines for all tests.
testlist:
	@grep --text '^##' *.tst *.sh
	@grep --text '^ ##' *.svn
listcheck:
	@for f in *.tst *.svn; do \
	    if ( head -3 $$f | grep --text -q '^ *##' ); then :; else echo "$$f needs a description" >&2; exit 1; fi;  \
	done

# BEGINS: Tests that require only reposurgeon itself

# Test that all stream files round-trip properly: compressed, uncompressed,
# and when passed through as message boxes by msgin/msgout.
ROUNDTRIP_TARGETS=$(patsubst %,roundtrip-%,$(wildcard *.fi))
ROUNDTRIP_COMPRESS_TARGETS=$(patsubst %,roundtrip-compress-%,$(wildcard *.fi))
MESSAGEBOX_TARGETS=$(patsubst %,messagebox-%,$(wildcard *.fi))

# Stream roundtripping tests
$(ROUNDTRIP_TARGETS): roundtrip-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "read <$<; write -" | tapdiffer "$< roundtrip" $<) || echo "$@" >>$(FAILLOG);
$(ROUNDTRIP_COMPRESS_TARGETS): roundtrip-compress-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "set flag compress" "read -; write -" <$< | tapdiffer "$< compressed roundtrip" $<) || echo "$@" >>$(FAILLOG);
$(MESSAGEBOX_TARGETS): messagebox-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "read <$<" "msgout >/tmp/msgbox$$$$" "msgin </tmp/msgbox$$$$" "write -" "shell rm /tmp/msgbox$$$$" | tapdiffer "$< message-box roundtripping" $<) || echo "$@" >>$(FAILLOG);

# Generated command-test loads
GENERATEDSVN=multigen.svn ignore.svn global-ignores.svn ignore-copy.svn global-ignores-copy.svn tagdoublet.svn \
	mixedbranchlift.svn tagwithcommits.svn branchtagdoublet.svn multiprojectmerge.svn branchrenametipdelete.svn \
	branchcreateempty.svn
GENERATEDGIT=bubblegen.fi
ignore.svn: ignoregen.sh
	sh $< -o $@ ignore
global-ignores.svn: ignoregen.sh
	sh $< -o $@ global-ignores
ignore-copy.svn: ignoregen.sh
	sh $<  -o $@ ignore copy
global-ignores-copy.svn: ignoregen.sh
	sh $< -o $@ global-ignores copy

clobber:
	rm -f $(GENERATEDSVN) $(GENERATEDGIT)
regenerate: $(GENERATEDSVN) $(GENERATEDGIT)

# General regression testing of commands and output; look at the *.tst and
# corresponding *.chk files to see which tests this runs.
COMMAND_LOADS := $(shell ls -1 *.tst | sed '/.tst/s///')
COMMAND_TARGETS=$(COMMAND_LOADS:%=command-check-%)
$(COMMAND_TARGETS): command-check-%: %.tst
	@singletest -a testing123 $< || echo "$@" >>$(FAILLOG);
command-buildcheck: $(GENERATEDSVN) $(GENERATEDGIT)
	@for file in $(COMMAND_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    singletest -q -b -a testing123 $${file}; \
	done

# Regression-test loading from Subversion
SVN_AND_TST_FILES := $(sort $(wildcard *.svn *.tst))
SVNLOAD_LOADS := $(shell echo $(SVN_AND_TST_FILES) | sed 's/\([^ ]*\)\.svn \1.tst//g; s/[^ ]*\.tst//g; s/\.svn//g')
svnload-buildcheck:
	@for test in $(SVNLOAD_LOADS); do \
	    if [ -f $${test}.tst ] ; \
	    then \
		    echo "BUG: $${test} should have been skipped: $${test}.tst exists"; \
		    exit 1; \
		fi; \
		echo "Remaking $${test}.chk"; \
		rm -f $${test}.chk && $(MAKE) --quiet $${test}.chk || exit 1; \
	done
SVNLOAD_TARGETS=$(SVNLOAD_LOADS:%=svnload-check-%)
$(SVNLOAD_TARGETS): svnload-check-%: %.svn
	@(test=$(<:.svn=); legend=$$(sed -n '/^ ## /s///p' <"$<" 2>/dev/null || echo "(no description)"); \
	$(REPOSURGEON) "$(TESTOPT)" "$(TESTOPT)" "read <$${test}.svn" "prefer git" "write -"  >/tmp/stream$$$$ 2>&1; \
	tapdiffer "$<: $${legend}" $${test}.chk </tmp/stream$$$$ ) || echo "$@" >>$(FAILLOG);

# Regression-testing of repocutter
REPOCUTTER_LOADS := $(shell ls -1 repocutter*.sh | sed '/.sh$$/s///')
repocutter-buildcheck:
	@for file in $(REPOCUTTER_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit 1; \
	done
REPOCUTTER_TARGETS = $(REPOCUTTER_LOADS:%=repocutter-test-%)
$(REPOCUTTER_TARGETS): repocutter-test-%:
	@(legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh | tapdiffer "$${legend}" $*.chk) || echo "$@" >>$(FAILLOG);

# Regression-testing of repomapper; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
REPOMAPPER_LOADS := $(shell ls -1 repomapper*.sh | sed '/.sh/s///')
repomapper-buildcheck:
	@for file in $(REPOMAPPER_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit 1; \
	done
REPOMAPPER_TARGETS = $(REPOMAPPER_LOADS:%=mapper-test-%)
$(REPOMAPPER_TARGETS): mapper-test-%:
	@(legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh | tapdiffer "$${legend}" $*.chk) || echo "$@" >>$(FAILLOG);

# Test legacy-map generation
LEGACY_LOADS=nut mergeinfo-with-split
legacy-buildcheck:
	@for test in $(LEGACY_LOADS); \
	do \
	    echo "Remaking $${test}.map"; \
	    rm -f $${test}.map && $(MAKE) --quiet $${test}.map 2>/dev/null || exit 1; \
	done
LEGACY_TARGETS = $(LEGACY_LOADS:%=legacy-test-%)
$(LEGACY_TARGETS): legacy-test-%: %.svn
	@(legend=$$(sed -n '/^ ## /s///p' <"$<" 2>/dev/null || echo "(no description)"); \
	$(REPOSURGEON) "$(TESTOPT)" "read <$<" "legacy write -" | grep -v '^reposurgeon:' 2>/dev/null \
	| tapdiffer "$${legend}" $(basename $<).map) || echo "$@" >>$(FAILLOG);

# ENDS: Tests that require only reposurgeon itself

# BEGINS: Tests requiring svn but not git

ifneq ($(call AVAILABLE svn),)

# Regression-testing of Subversion permission-bit and other exceptional cases.
# These tests are actually here to check Subversion's behavior, in case something
# poorly documented changes.
SVNCHECK_LOADS := $(shell ls -1 svncheck*.sh | sed '/.sh/s///')
svncheck-buildcheck:
	@for file in $(SVNCHECK_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set flag fakeuser" 'read -' 'prefer git' 'write -' >$${file}.chk || exit 1; \
	done
SVNCHECK_TARGETS = $(SVNCHECK_LOADS:%=svncheck-test-%)
$(SVNCHECK_TARGETS): svncheck-test-%:
	@(legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set flag fakeuser" 'read -' 'prefer git' 'write -' \
	| tapdiffer "$*: $${legend}" $*.chk) || echo "$@" >>$(FAILLOG);

endif # if svn

# ENDS: Tests requiring svn but not git

# All tests past this point reqire Git

ifneq ($(call AVAILABLE git),)

# BEGINS: Tests requiring svn and git

ifneq ($(call AVAILABLE svn),)

# Test Subversion conversion correctness
LIFTCHECK_LOADS = $(wildcard *.svn)
LIFTCHECK_TARGETS = $(LIFTCHECK_LOADS:%=liftcheck-check-full-%)
$(LIFTCHECK_TARGETS): liftcheck-check-full-%: %
	@liftcheck -q -r all $< || echo "$@" >>$(FAILLOG);

endif # if svn

# ENDS: Tests requiring svn and git

# BEGINS: Tests requiring git only

# Test the git extractor
GIT_EXTRACTOR_LOADS= bs.fi testrepo2.fi utf8.fi
GIT_EXTRACTOR_TARGETS = $(GIT_EXTRACTOR_LOADS:%=git-extractor-test-%)
$(GIT_EXTRACTOR_TARGETS): git-extractor-test-%: %
	@(./fi-to-fi <$< | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon/d' \
	| tapdiffer "$<: git extractor round-tripping" $<) || echo "$@" >>$(FAILLOG);

# ENDS: Tests requiring git only

# BEGINS: Tests requiring hg

ifneq ($(call AVAILABLE hg),)

# Test the hg support
HGSTREAMS = testrepo2
HGSTREAMS_EXTRACTOR_TARGETS = $(HGSTREAMS:%=hg-extractor-test-%)
$(HGSTREAMS_EXTRACTOR_TARGETS): hg-extractor-test-%: %.fi
	@(./hg-to-fi <$< | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon/d' \
	| tapdiffer "$<: hg extractor" $<) || echo "$@" >>$(FAILLOG);
HGSCRIPTS = $(subst -test,,$(subst hg-,,$(wildcard hg-*-test)))
hg-buildcheck:
	@for item in $(HGSCRIPTS); do \
	    echo "Remaking $${item}.fi"; \
	    ./hg-$${item}-test | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${item}.fi \
		2>&1 || exit 1; \
	done
HG_EXTRACTOR_TARGETS=$(HGSCRIPTS:%=hg-extractor-test-%)
$(HG_EXTRACTOR_TARGETS): hg-extractor-test-%: %.fi
	@(./hg-$*-test | sed -e 1d -e '/^#legacy-id/d'  | sed -e '/^#reposurgeon/d' \
	| tapdiffer "$<: hg extractor round-tripping" $<) || echo "$@" >>$(FAILLOG)

endif # if hg

# ENDS: Tests requiring hg

endif # if git

# BEGINS: Tests requiring bzr

ifneq ($(call AVAILABLE bzr),)

BZR_TARGETS=bzr-test-check
bzr-test-check:
	@(./export-test.sh bzr | tapdiffer "bzr export test" bzrtest.chk) || echo "$@" >>$(FAILLOG)
bzr-buildcheck:
	@./export-test.sh bzr >bzrtest.chk

endif # if bzr

# ENDS: Tests requiring bzr

# BEGINS: Tests requiring brz

ifneq ($(call AVAILABLE brz),)

BRZ_TARGETS=brz-test-check
brz-test-check:
	@(./export-test.sh brz | tapdiffer "brz export test" brztest.chk) || echo "$@" >>$(FAILLOG)
brz-buildcheck:
	@./export-test.sh brz >brztest.chk

endif # if brz

# ENDS: Tests requiring brz

# BEGINS: Tests requiring darcs

ifneq ($(call AVAILABLE darcs),)

DARCS_TARGETS=darcs-test-check
darcs-test-check:
	@(./export-test.sh darcs | tapdiffer "darcs export test" darcstest.chk) || echo "$@" >>$(FAILLOG)
darcs-buildcheck:
	@./export-test.sh darcs >darcstest.chk

endif # if darcs

# ENDS: Tests requiring brz

# BEGINS: Tests requiring fossil

ifneq ($(call AVAILABLE fossil),)

FOSSIL_TARGETS=fossil-test-check
fossil-test-check:
	@(./export-test.sh fossil | tapdiffer "fossil export test" fossiltest.chk) || echo "$@" >>$(FAILLOG)
fossil-buildcheck:
	@./export-test.sh fossil >fossiltest.chk

endif # if fossil

# ENDS: Tests requiring fossil

# Tests past this point may depend on any VCS and are expected
# to die cleanly with a TAP complaint if they can't find what they need.

# Regression-testing of repotool; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
REPOTOOL_LOADS := $(shell ls -1 repotool*.sh | sed '/.sh/s///')
repotool-buildcheck:
	@for file in $(REPOTOOL_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh --rebuild || exit 1; \
	done
REPOTOOL_TARGETS = $(REPOTOOL_LOADS:%=repotool-test-%)
$(REPOTOOL_TARGETS): repotool-test-%:
	@(legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh --regress) || echo "$@" >>$(FAILLOG);

# Miscellaneous tests.
SPORADIC_LOADS := hashcheck ignoretest workflow-cvs-git workflow-svn-git incrementalcheck svndircopyprop
SPORADIC_TARGETS = $(SPORADIC_LOADS:%=sporadic-test-%)
$(SPORADIC_TARGETS): sporadic-test-%:
	@$(SHELL) $*.sh || echo "$@" >>$(FAILLOG);

# All test targets. Putting expensive targets early in this list might
# shave a little off total time. The Subversion tools are much the slowest
# so those tests start first.
TEST_TARGETS = $(LIFTCHECK_TARGETS) $(SVNCHECK_TARGETS) \
		$(GIT_EXTRACTOR_TARGETS) $(HGSTREAMS_EXTRACTOR_TARGETS) $(HG_EXTRACTOR_TARGETS) \
		$(BZR_TARGETS) $(BRZ_TARGETS) $(DARCS_TARGETS) $(FOSSIL_TARGETS) \
		$(ROUNDTRIP_TARGETS) $(ROUNDTRIP_COMPRESS_TARGETS) $(MESSAGEBOX_TARGETS) \
		$(COMMAND_TARGETS) $(SVNLOAD_TARGETS) $(LEGACY_TARGETS) \
		$(REPOMAPPER_TARGETS) $(REPOTOOL_TARGETS) $(REPOCUTTER_TARGETS) \
		$(SPORADIC_TARGETS)

# Here's how we actually run the tests:

# Issue a straight TAP report, no parallelism or filtering
tap: clean count $(TEST_TARGETS)
	@echo "# Tests complete"
count:
	@echo 1..$(words $(TEST_TARGETS))

# This is the normal way to run the tests interactively,
# using a TAP consumer.
parallel-tap: clean
	@rm -f $(FAILLOG)
	@$(call TAPOUT,$(TEST_TARGETS)); if [ -s $(FAILLOG) ]; then ( echo "Tests failed:"; cat $(FAILLOG); exit 1); fi; 

# This is how to rebuild all the check files.  It will crap out with
# a complaint about an undefined production if any of the sectioned
# conditioned on rtools being available is not included.  We want
# noisy failure here because the alternative is that checkfiles might
# silently not get updated, leadin to vast confusion later.
buildcheck: command-buildcheck svnload-buildcheck legacy-buildcheck \
	hg-buildcheck bzr-buildcheck brz-buildcheck darcs-buildcheck fossil-buildcheck repomapper-buildcheck \
	repotool-buildcheck repocutter-buildcheck svncheck-buildcheck 

# Test productions end here.

# The result from this is checked in because, as it turns out, the order
# in which the components are archived can vary randomly based on the
# state of the filesystem when it is built.
make-tarball:
	mkdir -p /tmp/tarball
	echo "first sample small file" >/tmp/tarball/snip
	echo "second sample small file" >/tmp/tarball/snap
	chmod a+x /tmp/tarball/snap
	ln -s /tmp/tarball/snip /tmp/tarball/alias
	here=`pwd`
	(cd /tmp; tar cf sample.tar tarball)
	mv /tmp/sample.tar .
make-tarball2:
	mkdir -p /tmp/tarball
	echo "first sample2 small file" >/tmp/tarball/bim
	echo "second sample2 small file" >/tmp/tarball/bam
	here=`pwd`
	(cd /tmp; tar cf sample2.tar tarball)
	mv /tmp/sample2.tar .

# end
