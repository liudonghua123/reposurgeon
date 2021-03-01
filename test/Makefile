# Test-suite makefile for reposurgeon

# By setting the REPOSURGEON environment variable and using -e
# a different implementation can be plugged in to the tests.

# Use absolute path so tests that change working directory still use 
# scripts from parent directory.  Note that using $PWD seems to fail
# here under Gitlab's CI environment.
PATH := $(BINDIR):$(realpath ..):$(realpath .):${PATH}
REPOSURGEON := reposurgeon 
REPOCUTTER := repocutter 
REPOTOOL := repotool
REPOMAPPER := repomapper

# Setting this to 1 suppresses detail sections in most TAP reportts.
QUIET=0

# The TAP filter. Only affects presentation of the test suite messages
TAPCONSUMER=tappy

# Force pure serial execution when rebuilding check files.  Slower,
# but makes them deterministic and may help smoke out bugs in
# concurrent code.
BUILDOPT="set serial"

# No user-serviceable parts below this line.

# Force the timezone in case CI has a different idea
export TZ=UTC

# Define test groups
BASIC = listcheck roundtrip-regress command-regress
SUBVERSION = svnload-regress liftcheck-regress legacy-regress svncheck-regress
GIT_EXTRACTOR = git-regress
HG_EXTRACTOR = hg-regress
AUXTOOLS = repocutter-regress repomapper-regress repotool-regress

# See https://stackoverflow.com/questions/6481005/how-to-obtain-the-number-of-cpus-cores-in-linux-from-the-command-line
CONCURRENT_JOBS=$(shell getconf _NPROCESSORS_ONLN || getconf NPROCESSORS_ONLN || echo 4)

# Fall back to safety if our declared TAP consumer does not exist.
# This is helpful in the CI environment, where it wiuld be better for
# the logfiles to carry the raw TAP messages. 
TAPFILTER=$(shell command -v $(TAPCONSUMER) || echo cat)

# Run specified tests in parallel and ship their output to our TAP consumer after a plan header
TAPOUT = (echo "1..$(words $(1))"; $(MAKE) --output-sync --no-print-directory -j $(CONCURRENT_JOBS) $(1)) | $(TAPFILTER)

# Test whether we have a specified command
AVAILABLE=command -v $(1) >/dev/null 2>&1

# check-aux must not be run in parallel.
check:
	$(MAKE) -j1 check-aux

# This should always be a full test
check-aux: clean disclose $(BASIC) $(SUBVERSION) $(GIT_EXTRACTOR) $(HG_EXTRACTOR) $(AUXTOOLS) sporadic-regress
	@echo "=== No diff output is good news ==="

disclose:
	@echo "Running with $(CONCURRENT_JOBS) threads."

.SUFFIXES: .svn .chk .fi .map

.svn.chk:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@ 2>&1
.svn.fi:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@
.svn.map:
	$(REPOSURGEON) "log -all" "read <$<" "legacy write -" >$@

buildregress: command-buildregress svnload-buildregress legacy-buildregress \
	hg-buildregress repomapper-buildregress repotool-buildregress repocutter-buildregress \
	svncheck-buildregress 

clean:
	rm -fr .rs* test=repo test-checkout test-repo* test-checkout* git-repo left-repo right-repo *~

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

# The way these test productions work is kind of hairy, so the
# constraints and assumptions are documented here.  The biggest
# constraint is that we need a test failure to pass out a status that
# terminates make - this is required for CI to notice the failure, as
# it does not patthern-match on the output of the tests.
#
# These productions generally come in pairs. The outer one, meant to
# be invoked, issues a banner and invokes parallel make on a distimctive
# set of target pattern names.  The inner one is a pattern rule that
# describes how to reach a target matching the pattern.
#
# A consequence of this organization is that in order to force
# termination of the outer rule with error status 1, an inner rule
# cannot merely return $?=1, it has to do an explicit exit 1.

# Test that all stream files round-trip properly: compressed, uncompressed,
# and when passed through as message boxes by msgin/msgout.
RULES_ROUNDTRIP=$(patsubst %,roundtrip-%,$(wildcard *.fi))
ROUNDTRIP_COMPRESS_TARGETS=$(patsubst %,roundtrip-compress-%,$(wildcard *.fi))
RULES_MESSAGE_BOX=$(patsubst %,messagebox-%,$(wildcard *.fi))

# Stream riundtripping tests
$(ROUNDTRIP_TARGETS): roundtrip-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "read -; write -" <$< | QUIET=$(QUIET) tapdiffer "$< roundtrip" $<)
$(ROUNDTRIP_COMPRESS_TARGETS): roundtrip-compress-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "set compress" "read -; write -" <$< | QUIET=$(QUIET) tapdiffer "$< compressed roundtrip" $<)
$(RULES_MESSAGE_BOX): messagebox-%: %
	@$(REPOSURGEON) "$(TESTOPT)" "read <$<" "msgout >/tmp/msgbox$$$$" "msgin </tmp/msgbox$$$$" "write -" "shell rm /tmp/msgbox$$$$" | QUIET=$(QUIET) tapdiffer "$< message-box roundtripping" $<
roundtrip-regress:
	@echo "=== Testing stream-file round-tripping:"
	@$(call TAPOUT, $(ROUNDTRIP_TARGETS) $(ROUNDTRIP_COMPRESS_TARGETS) $(RULES_MESSAGE_BOX))

# Generated command-test loads
multigen.svn: multigen.sh
	sh multigen.sh -d >multigen.svn
ignore.svn: ignoregen.sh
	sh ignoregen.sh ignore >ignore.svn
global-ignores.svn: ignoregen.sh
	sh ignoregen.sh global-ignores >global-ignores.svn
ignore-copy.svn: ignoregen.sh
	sh ignoregen.sh ignore copy >ignore-copy.svn
global-ignores-copy.svn: ignoregen.sh
	sh ignoregen.sh global-ignores copy >global-ignores-copy.svn
tagdoublet.svn: tagdoubletgen.sh
	sh tagdoubletgen.sh >tagdoublet.svn
mixedbranchlift.svn: branchliftgen.sh
	sh branchliftgen.sh >mixedbranchlift.svn

# General regression testing of commands and output; look at the *.tst and
# corresponding *.chk files to see which tests this runs.
COMMAND_LOADS := $(shell ls -1 *.tst | sed '/.tst/s///')
COMMAND_TARGETS=$(COMMAND_LOADS:%=command-regress-%)
$(COMMAND_TARGETS): command-regress-%: %.tst
	@QUIET=$(QUIET) singletest -a testing123 $<;
command-buildregress: multigen.svn ignore.svn global-ignores.svn tagdoublet.svn 
	@for file in $(COMMAND_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(REPOSURGEON) $(BUILDOPT) "$(TESTOPT)" "script $${file}.tst testing123" >$${file}.chk \
		2>&1 || exit 1; \
	done
command-regress: multigen.svn ignore.svn global-ignores.svn ignore-copy.svn global-ignores-copy.svn mixedbranchlift.svn
	@echo "=== Running general tests:"
	@$(call TAPOUT,$(COMMAND_TARGETS))

# The goal of filtering with a TAP consumer is to keep the error
# display small enough that when any one test gets broken it's
# guaranteed not to get lost in a flood of routine messages or scroll
# off the screen.  This puts a premium on having as few as possible
# TAP output frames (each correspomding to one invocation of the TAP
# consumer) as possible.  On the other hand, long lag in the middle of
# a test group makes for uncertainty about whether some test might be
# hanging, which is bad. Choosing the optimal grouping requires some
# sensitivity to the distribution of test running times and of failure
# risk.
#
# Trying to cut down on the number of TAP reporting frames leads us down 
# one serious rabbit hole.  The sticky case is when we want to supply both a
# test-load name and a description of what case it excercises for
# generating a TAP status message, and we cam't mine the descrption
# out of the test load.
#
# The following horrible hackery seems to be the only way to pass a
# list of (filestem, description) pairs to make in such a way that we
# can separate the pair components and use them to generate a custom
# rule.  Here's how it works:
#
# 1. Start with a list of {filestem, description} pairs glued together
# in a way that makes them syntactically legal to be make rule
# targets.  We separate filestem from description with a period,
# because that will help us separate the parts later. We use the
# standard dodge of encoding spaces as underscres.
#
# 4. Declare a target list. This will be the list you declared in step 1
# with a distinguishing namespace prefix added to each member.
#
# 3. Write a pattern rule with that distinguishing nanespace prefix
# before %.  The rule text will have one variable part - #@, the
# target name.  Now the real fun starts: and to make it more fun,
# we're going to use several of make's more obscure features to
# generate as much of the command text as possible early, rather than
# using shell hackery at rule execution time.
#
# The reason we want this is simple: so that make -n will show us
# something with a reasonably clear relationship to what's finally
# executed.  Otherwise trying to debug and extend these rules would be
# a fast path to insanity.
#
# 4. First, we need to the prefix off $@.  Then we need to break our 
# {filestem, description} cookie apart.  This is where it matters that we used
# a period as a separator, because it turns out that the only functions in
# make's string library that can split up a token are $(prefix) and $(suffix)
# intended for strings like "foo.c".
#
# Complicating matters, expansion of user-defined make variables is
# either unreliable or has undocumented restrictions, so both of the
# part-extractor expressions have to be written in one fell swoop
# starting from $@.
#
# 5. The payoff from all this is that we cn now feed a simple derivative
# of our original list of {filestem, description} pairs to a parallel make
# and the right things will happen,
#
# To explore the following worked example, do "make -n git-regress" to
# see the parallel-make command that gets generated; the interesting
# part is the kist if targets.  Then do "make -n
# git-regress-test-be2.with_merges" to see how a target in the list
# expands to a generared rule.  Finally do "make TAPCONSUMER=cat
# git-regress" to see what execution of the generated targets looks
# like.

# Test the git extractor
GIT_EXTRACTOR_LOADS= \
	bs.basic \
	be.with_multiple_git_branches \
	bs.with_multiple_git_branches \
	be2.with_merges \
	be3.with_merges \
	be4.with_merges \
	be5.with_merges \
	bt.with_tags \
	bt2.with_tags
GIT_EXTRACTOR_TARGETS = $(GIT_EXTRACTOR_LOADS:%=git-extractor-test-%)
$(GIT_EXTRACTOR_TARGETS): git-extractor-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	filestem="$(basename $*)"; description="$(subst _, ,$*)"; \
	./fi-to-fi <$${filestem}.fi | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon/d' \
	| QUIET=$(QUIET) tapdiffer "$${filestem}.fi: git extractor $${description}" $${filestem}.fi;
git-regress:
	@echo "=== Testing git-extractor:"
	@if $(call AVAILABLE git) ; \
	then \
		$(call TAPOUT,$(GIT_EXTRACTOR_TARGETS)); \
	else echo "    Skipped, git missing."; exit 0; \
	fi

# Test the hg supprt
HGSTREAMS = testrepo2
HGSTREAMS_EXTRACTOR_TARGETS = $(HGSTREAMS:%=hg-extractor-test-%)
$(HGSTREAMS_EXTRACTOR_TARGETS): hg-extractor-test-%: %.fi
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; \
	./hg-to-fi <$< | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon/d' \
	| QUIET=$(QUIET) tapdiffer "$<: hg extractor" $<;
HGSCRIPTS = $(subst -test,,$(subst hg-,,$(wildcard hg-*-test)))
hg-buildregress:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
	for item in $(HGSCRIPTS); do \
	    echo "Remaking $${item}.fi"; \
	    ./hg-$${item}-test | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${item}.fi \
		2>&1 || exit 1; \
	done
HG_EXTRACTOR_LOADS= \
	be.with_multiple_hg_branches \
	be2.with_multiple_hg_branches \
	be3.with_multiple_hg_branches \
	bb-alt.with_multiple_hg_branches_and_impossible_coloring \
	be-bookmarks.with_multiple_hg_branches \
	be2.with_merges \
	be3.with_merges \
	be4.with_merges \
	be5.with_merges \
	be6.with_merges \
	bt.with_tags \
	bt2.with_tags \
	bt3.with_tags \
	bt3-local.with_tags \
	be-subrepo.with_pathological_loads \
	be-subrepo2.with_pathological_loads
HG_EXTRACTOR_TARGETS=$(HG_EXTRACTOR_LOADS:%=hg-extractor-test-%)
hg-extractor-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	filestem="$(basename $*)"; description="$(subst _, ,$(subst .,,$(suffix $*)))"; \
	./hg-$${filestem}-test | sed -e 1d -e '/^#legacy-id/d'  | sed -e '/^#reposurgeon/d' \
	| QUIET=$(QUIET) tapdiffer "$${filestem}.fi: hg extractor $${description}" $${filestem}.fi
hg-regress:
	@echo "=== Testing hg extractor:"
	@if $(call AVAILABLE hg) && $(call AVAILABLE git) ; \
	then \
		$(call TAPOUT,$(HGSTREAMS_EXTRACTOR_TARGETS) $(HG_EXTRACTOR_TARGETS)); \
	else echo "    Skipped, hg or git missing."; exit 0; \
	fi

# Test loading from Subversion
SVN_AND_TST_FILES := $(sort $(wildcard *.svn *.tst))
SVNLOAD_LOADS := $(shell echo $(SVN_AND_TST_FILES) | sed 's/\([^ ]*\)\.svn \1.tst//g; s/[^ ]*\.tst//g; s/\.svn//g')
svnload-buildregress:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	for test in $(SVNLOAD_LOADS); do \
	    if [ -f $${test}.tst ] ; \
	    then \
		    echo "BUG: $${test} should have been skipped: $${test}.tst exists"; \
		    exit 1; \
		fi; \
		echo "Remaking $${test}.chk"; \
		rm -f $${test}.chk && $(MAKE) --quiet $${test}.chk || exit 1; \
	done
SVN_LOAD_TARGETS=$(SVNLOAD_LOADS:%=svnload-regress-%)
$(SVN_LOAD_TARGETS): svnload-regress-%: %.svn
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
	test=$(<:.svn=); legend=$$(sed -n '/^ ## /s///p' <"$<" 2>/dev/null || echo "(no description)"); \
	$(REPOSURGEON) "$(TESTOPT)" "$(TESTOPT)" "read <$${test}.svn" "prefer git" "write -"  >/tmp/stream$$$$ 2>&1; \
	QUIET=$(QUIET) tapdiffer "$<: $${legend}" $${test}.chk </tmp/stream$$$$;
svnload-regress:
	@echo "== Testing loading of Subversion streams:"
	@$(call TAPOUT, $(SVN_LOAD_TARGETS))

# Test Subversion conversion correctness
LIFTCHECK_LOADS = $(wildcard *.svn)
LIFTCHECK_TARGETS = $(LIFTCHECK_LOADS:%=liftcheck-regress-full-%)
$(LIFTCHECK_TARGETS): liftcheck-regress-full-%: %
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; \
	liftcheck -q -r all $<
liftcheck-regress:
	@echo "== Testing conversion correctness of SVN histories:"
	@if  $(call AVAILABLE git) &&  $(call AVAILABLE svn); \
	then \
		$(call TAPOUT, $(LIFTCHECK_TARGETS)); \
	else \
		echo "     Skipped, either git or svn is missing"; \
	fi

# Test legacy-map generation
LEGACY_LOADS=nut mergeinfo-with-split
legacy-buildregress:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	for test in $(LEGACY_LOADS); \
	do \
	    echo "Remaking $${test}.map"; \
	    rm -f $${test}.map && $(MAKE) --quiet $${test}.map 2>/dev/null || exit 1; \
	done
LEGACY_TARGETS = $(LEGACY_LOADS:%=legacy-test-%)
$(LEGACY_TARGETS): legacy-test-%: %.svn
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	legend=$$(sed -n '/^ ## /s///p' <"$<" 2>/dev/null || echo "(no description)"); \
	$(REPOSURGEON) "$(TESTOPT)" "read <$<" "legacy write -" | grep -v '^reposurgeon:' 2>/dev/null \
	| QUIET=$(QUIET) tapdiffer "$${legend}" $(basename $<).map
legacy-regress:
	@echo "=== Testing legacy-map generation:"
	@$(call TAPOUT,$(LEGACY_TARGETS));

# Regression-testing of repomapper; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
REPOMAPPER_LOADS := $(shell ls -1 repomapper*.sh | sed '/.sh/s///')
repomapper-buildregress:
	@REPOMAPPER=$$REPOMAPPER; export REPOMAPPER; \
	for file in $(REPOMAPPER_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit 1; \
	done
REPOMAPPER_TARGETS = $(REPOMAPPER_LOADS:%=mapper-test-%)
$(REPOMAPPER_TARGETS): mapper-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh | QUIET=$(QUIET) tapdiffer "$${legend}" $*.chk;
repomapper-regress:
	@echo "=== Running repomapper tests:"
	@$(call TAPOUT,$(REPOMAPPER_TARGETS))

# Regression-testing of repotool; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
REPOTOOL_LOADS := $(shell ls -1 repotool*.sh | sed '/.sh/s///')
repotool-buildregress:
	@REPOTOOL=$$REPOTOOL; export REPOTOOL; \
	for file in $(REPOTOOL_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh --rebuild || exit 1; \
	done
REPOTOOL_TARGETS = $(REPOTOOL_LOADS:%=repotool-test-%)
$(REPOTOOL_TARGETS): repotool-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh --regress
repotool-regress:
	@echo "=== Running repotool tests:"
	@$(call TAPOUT,$(REPOTOOL_TARGETS))

# Regression-testing of repocutter
REPOCUTTER_LOADS := $(shell ls -1 repocutter*.sh | sed '/.sh/s///')
repocutter-buildregress:
	@REPOMAPPER=$$REPOMAPPER; export REPOMAPPER; \
	for file in $(REPOCUTTER_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit 1; \
	done
REPOCUTTER_TARGETS = $(REPOCUTTER_LOADS:%=repocutter-test-%)
$(REPOCUTTER_TARGETS): repocutter-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh | QUIET=$(QUIET) tapdiffer "$${legend}" $*.chk;
repocutter-regress:
	@echo "=== Running repocutter tests:"
	@$(call TAPOUT,$(REPOCUTTER_TARGETS))

# Regression-testing of Subversion permission-bit and other exceptional cases.
# These tests are actually here to check Subversion's behavior, in case something
# poorly documented changes
SVNCHECK_LOADS := $(shell ls -1 svncheck*.sh | sed '/.sh/s///')
svncheck-buildregress:
	@for file in $(SVNCHECK_LOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set testmode" 'read -' 'prefer git' 'write -' >$${file}.chk || exit 1; \
	done
SVNCHECK_TARGETS = $(SVNCHECK_LOADS:%=svncheck-test-%)
$(SVNCHECK_TARGETS): svncheck-test-%:
	legend=$$(sed -n '/^## /s///p' <"$*.sh" 2>/dev/null || echo "(no description)"); \
	$(SHELL) $*.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set testmode" 'read -' 'prefer git' 'write -' \
	| tapdiffer "$*: $${legend}" $*.chk;
svncheck-regress:
	@echo "=== Testing svncheck-regress:"
	@if $(call AVAILABLE svn) ; \
	then \
		$(call TAPOUT,$(SVNCHECK_TARGETS)); \
	else echo "    Skipped, svn missing."; exit 0; \
	fi

# Miscellaneous tests.
SPORADIC_LOADS := hashcheck workflow-cvs-git workflow-svn-git
SPORADIC_TARGETS = $(SPORADIC_LOADS:%=super-test-%)
$(SPORADIC_TARGETS): super-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	$(SHELL) $*.sh;
sporadic-regress:
	@echo "=== Running super tests:"
	@$(call TAPOUT,$(SPORADIC_TARGETS))

# Test productions end here.

# The result from this is checked in because, as it turns out, the order
# in which the components are archived can vary randomly based on the
# state of the filesystem when it is built.
make-tarball:
	mkdir -p /tmp/tarball
	echo "first sample small file" >/tmp/tarball/snip
	echo "second sample small file" >/tmp/tarball/snap
	chmod a+x /tmp/tarball/snap
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

# Make a new repo and checkout directory to create a Subversion test load.
# Reminder - a branch copy looks like this:
# svn copy file://$PWD/test-repo/trunk file://$PWD/test-repo/branches/foo
# or, within test-checkout, svn copy ^/trunk ^/branches/foo
svn-flat: clean
	svnadmin create test-repo
	svn co file://${PWD}/test-repo test-checkout
svn-branchy: svn-flat
	@cd test-checkout || exit 1; set -x; \
		svn mkdir trunk || exit 1; \
		svn mkdir tags || exit 1; \
		svn mkdir branches || exit 1; \
		echo "Directory layout." | svn commit -F - || exit 1; \
		echo "This is a test Subversion repository" >trunk/README; \
		svn add trunk/README || exit 1; \
		echo "Initial README content." | svn commit -F - || exit 1
svn-dump:
	@svnadmin dump test-repo 

# end
