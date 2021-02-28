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

# Setting this to 0 allows tests to continue on error.
STOPOUT=1

# Setting this to 1 suppresses diffs in favor of a FAIL tag
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

# Defeat annoying behavior under Mac OS X - builtin echo doesn't do -n
ECHO := /bin/echo

# Define test groups
BASIC = listcheck roundtrip-regress command-regress
SUBVERSION = svnload-regress liftcheck-regress legacy-regress svncheck-regress liftcheck-fullregress
GIT_EXTRACTOR = git-regress
HG_EXTRACTOR = hg-regress hg-regress-branches hg-regress-merges hg-regress-tags \
	hg-regress-patho
AUXTOOLS = repocutter-regress repomapper-regress repotool-regress

# See https://stackoverflow.com/questions/6481005/how-to-obtain-the-number-of-cpus-cores-in-linux-from-the-command-line
CONCURRENT_JOBS=$(shell getconf _NPROCESSORS_ONLN || getconf NPROCESSORS_ONLN || echo 4)

# Exploit make parallelism
PARALLEL_MAKE=$(MAKE) --output-sync --no-print-directory -j $(CONCURRENT_JOBS)

# Generate a TAP plan line for a list of names given as arguments 
PLANLINE=echo "1..$(words $(1))"

# Fall back to safety if our declared TAP consumer does not exist.
# This is helpful in the CI environment, where it wiuld be better for
# the logfiles to carry the raw TAP messages. 
TAPFILTER=$(shell command -v $(TAPCONSUMER) || echo cat)

# Run tests in parallel and ship their output to our TAP consumer after a plan header
TAPOUT = (echo "1..$(words $(1))"; $(PARALLEL_MAKE) $(1)) | $(TAPFILTER)

# Test whether we have a specified command
AVAILABLE=command -v $(1) >/dev/null 2>&1

# check-aux must not be run in parallel.
check:
	$(MAKE) -j1 check-aux

# This should always be a full test
check-aux: clean disclose $(BASIC) $(SUBVERSION) $(GIT_EXTRACTOR) $(HG_EXTRACTOR) $(AUXTOOLS) sporadic
	@echo "=== No diff output is good news ==="

disclose:
	@echo "Running with $(CONCURRENT_JOBS) threads."

# FIXME: is log -all really correct when regenerating the map checks?

.SUFFIXES: .svn .chk .fi .map

.svn.chk:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@ 2>&1
.svn.fi:
	$(REPOSURGEON) "read <$<" "prefer git" "write -" >$@
.svn.map:
	$(REPOSURGEON) "log -all" "read <$<" "legacy write -" >$@

buildregress: fi-buildregress svnload-buildregress legacy-buildregress \
	repomapper-buildregress repotool-buildregress hg-buildregress-branches \
	svncheck-buildregress repocutter-buildregress repomapper-buildregress \
	repotool-buildregress

clean:
	rm -fr .rs* test-repo test-checkout git-repo left-repo right-repo *~

options:
	@echo "REPOSURGEON is '$(REPOSURGEON)'"
	@echo "TESTOPT is '$(TESTOPT)'"

# Show summary lines for all tests.
testlist:
	@grep --text '^##' *.tst *.sh
	@grep --text '^ ##' *.svn
listcheck:
	@for f in *.tst *.svn; do \
	    if ( head -3 $$f | grep --text -q '^ *##' ); then :; else echo "$$f needs a description" >&2; exit $(STOPOUT); fi;  \
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
# iermination of the outer rule with error status 1, an inner rule
# cannot merely return $?=1, it has to do an explicit exit 1. A
# significant amount of the complexity in these rules is the
# gymnastics required to make that exit value conditional on STOPOUT.
#
# Some pattern rules look like this: (GENERATOR | FILTER) || exit $(STOPOUT)
# The GENERATOR part pushers test output to a FILTER which compares it to
# a corresponding checkfile. If this pipeline fails the exit is called.
#
# Other test groups are implemented eith loops in a rool body; these can't
# use concurrency.  The difference is historical, not principled; all the
# test groups used to be written this way until one of our contributors
# got really interested in parallel make.

# Test that all stream files round-trip properly: compressed, uncompressed,
# and when passed through as message boxes by msgin/msgout.
RULES_ROUNDTRIP=$(patsubst %,roundtrip-%,$(wildcard *.fi))
RULES_ROUNDTRIP_COMPRESS=$(patsubst %,roundtrip-compress-%,$(wildcard *.fi))
RULES_MESSAGE_BOX=$(patsubst %,messagebox-%,$(wildcard *.fi))

$(RULES_ROUNDTRIP): roundtrip-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "read -; write -" <$< | QUIET=$(QUIET) tapdiffer "$< roundtrip" $<) || exit $(STOPOUT)
$(RULES_ROUNDTRIP_COMPRESS): roundtrip-compress-%: %
	@($(REPOSURGEON) "$(TESTOPT)" "set compress" "read -; write -" <$< | QUIET=$(QUIET) tapdiffer "$< compressed roundtrip" $<) || exit $(STOPOUT)
$(RULES_MESSAGE_BOX): messagebox-%: %
	@$(REPOSURGEON) "$(TESTOPT)" "read <$<" "msgout >/tmp/msgbox$$$$" "msgin </tmp/msgbox$$$$" "write -" "shell rm /tmp/msgbox$$$$" | QUIET=$(QUIET) tapdiffer "$< message-box roundtripping" $< || exit $(STOPOUT)

roundtrip-regress:
	@echo "=== Testing stream-file round-tripping:"
	@$(call TAPOUT, $(RULES_ROUNDTRIP) $(RULES_ROUNDTRIP_COMPRESS) $(RULES_MESSAGE_BOX))

# General regression testing of commands and output; look at the *.tst and
# corresponding *.chk files to see which tests this runs.
TESTLOADS := $(shell ls -1 *.tst | sed '/.tst/s///')

# Generated test loads
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

$(TESTLOADS:%=command-regress-%): command-regress-%: %.tst
	@QUIET=$(QUIET) singletest -a testing123 $< || exit $(STOPOUT)

command-regress: multigen.svn ignore.svn global-ignores.svn ignore-copy.svn global-ignores-copy.svn mixedbranchlift.svn
	@echo "=== Running general tests:"
	@$(call TAPOUT, $(TESTLOADS:%=command-regress-%))
command-buildregress: multigen.svn ignore.svn global-ignores.svn tagdoublet.svn 
	@for file in $(TESTLOADS); do \
	    echo "Remaking $${file}.chk"; \
	    $(REPOSURGEON) $(BUILDOPT) "$(TESTOPT)" "script $${file}.tst testing123" >$${file}.chk \
		2>&1 || exit $(STOPOUT); \
	done

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
# test-load name and a description of what case it excercises gor
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
# 2. Write a pattern rule with a distinguishing nanespace prefix
# before %.  The rule text will have one variable part - #@, the
# target name.  Now the real fun starts: and to make it more fun,
# we're going to use several of make's more obscure features to
# generate as much of the command text as possible early, rather than
# using shell hackery at rule execution time.
#
# The reason we want this is simple: so that make -n wil show us
# something with a reasonably clear relationship to what's finally
# executed.  Otherwise trying to debug and extend these rules would be
# a fast path to insanity.
#
# 3. First, we need to the prefix off $@.  Then we need to break our 
# {filestem, description} apart.  This is where it matters that we used
# a period as a separator, because it turns out that the only functions in
# make's string library that can split up a token are $(prefix) and $(suffix)
# intended for strings like "foo.c".
#
# Complicating matters, expansion of user-defined variables is either
# unreliable or has undocumented restrictions, so both of the
# part-extractor expressions have to be written in one fell swoop
# starting from $@.
#
# 5. The payoff from all this is that we cn now feed a simple derivative
# of our original list of {filestem, description} pairs to a parallel make
# and the right thubgs will happen,
#
# To explore the following worked example, do "make -n git-regress" to
# see the parallel-make cpmmand that gets generated; the interesting
# part is the kist if targets.  Then do "make -n
# git-regress-test-be2.with_merges" tio see how a target in the list
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
	bt2.with_tags \
	bt3.with_tags
git-extractor-test-%:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	filestem="$(basename $(subst git-extractor-test-,,$@))"; \
	description="$(subst _, ,$(subst .,,$(suffix $(subst git-extractor-test-,,$@))))"; \
	./fi-to-fi <$${filestem}.fi | sed -e 1d -e '/^#legacy-id/d' \
	| QUIET=$(QUIET) tapdiffer "$${filestem}.fi: git extractor $${description}" $${filestem}.fi
git-regress:
	@echo "=== Testing git-extractor:"
	@if $(call AVAILABLE git) ; \
	then \
		$(call TAPOUT, $(GIT_EXTRACTOR_LOADS:%=git-extractor-test-%)); \
	else echo "    Skipped, git missing."; exit 0; \
	fi

# Test the hg extractor
HGLOADS = testrepo2
hg-regress:
	@echo "=== Testing hg-extractor:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	if $(call AVAILABLE hg) && $(call AVAILABLE git) ; \
	then \
	    ($(call PLANLINE,$(HGLOADS)); \
	    for test in $(HGLOADS); do \
		./hg-to-fi <$${test}.fi | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' \
		| QUIET=$(QUIET) tapdiffer "$${test}: hg extractor" $${test}.fi; \
	    done) | $(TAPFILTER); \
	else echo "    Skipped, hg or git missing."; exit 0; \
	fi; \
	rm -f /tmp/regress-d$$$$

# Test the hg extractor with multiple hg branches
# NOTE: the bb-alt test demonstrates an hg repo with coloring that git cannot reproduce
HGBRANCHES = be be2 be3 bb-alt be-bookmarks
hg-buildregress-branches:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
	for file in $(HGBRANCHES); do \
	    echo "Remaking $${file}.fi"; \
	    ./hg-$${file}-test | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${file}.fi \
		2>&1 || exit $(STOPOUT); \
	done
hg-regress-branches:
	@echo "=== Testing hg-extractor with multiple hg branches:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	if $(call AVAILABLE hg); \
	then \
	    ($(call PLANLINE,$(HGBRANCHES)); \
	    for test in $(HGBRANCHES); do \
		./hg-$${test}-test | sed -e 1d -e '/^#legacy-id/d' \
		| QUIET=$(QUIET) tapdiffer "$${test}: multiple hg branches" $${test}.fi; \
	    done) | $(TAPFILTER); \
	else echo "    Skipped, hg missing."; exit 0; \
	fi; \
	rm -f /tmp/regress-e$$$$

# Test the hg extractor with merges
HGMERGES = be2 be3 be4 be5 be6
hg-buildregress-merges:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	for file in $(HGMERGES); do \
	    echo "Remaking $${file}.fi"; \
	    ./hg-$${file}-test | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${file}.fi \
		2>&1 || exit $(STOPOUT); \
	done
hg-regress-merges:
	@echo "=== Testing hg-extractor with merges:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	if $(call AVAILABLE hg) ; \
	then \
	    ($(call PLANLINE,$(HGMERGES)); \
	    for test in $(HGMERGES); do \
		./hg-$${test}-test | sed -e 1d -e '/^#legacy-id/d' \
		| QUIET=$(QUIET) tapdiffer "$${test}: multiple hg branches" $${test}.fi; \
	    done) | $(TAPFILTER); \
	else echo "    Skipped, hg missing."; exit 0; \
	fi; \
	rm -f /tmp/regress-f$$$$

# Test the hg extractor with tags
HGTAGS = bt bt2 bt3-hg bt3-hg-local
hg-buildregress-tags:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
	for file in $(HGTAGS); do \
	    echo "Remaking $${file}.fi"; \
	    ./hg-$${file}-test | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${file}.fi \
		2>&1 || exit $(STOPOUT); \
	done
hg-regress-tags:
	@echo "=== Testing hg-extractor with tags:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	if $(call AVAILABLE hg) ; \
	then \
	    ($(call PLANLINE,$(HGTAGS)); \
	    for test in $(HGTAGS); do \
		./hg-$${test}-test | sed -e 1d -e '/^#legacy-id/d' \
		| QUIET=$(QUIET) tapdiffer "$${test}: multiple hg branches" $${test}.fi; \
	    done) | $(TAPFILTER); \
	else echo "    Skipped, hg missing."; exit 0; \
	fi; \
	rm -f /tmp/regress-g$$$$

# Test the hg extractor with pathological loads
HGPATHO = be-subrepo be-subrepo2
hg-buildregress-patho:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
        ($(call PLANLINE,$(HGPATHO)); \
	for file in $(HGPATHO); do \
	    echo "Remaking $${file}.fi"; \
	    ./hg-$${file}-test 2>/dev/null | sed -e 1d -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' >$${file}.fi \
		2>&1 || exit $(STOPOUT); \
	done
hg-regress-patho:
	@echo "=== Testing hg-extractor with pathological loads:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	if $(call AVAILABLE hg) ; \
	then \
           ($(call PLANLINE,$(HGPATHO)); \
	   for test in $(HGPATHO); do \
		./hg-$${test}-test 2>/dev/null | sed -e '/^#legacy-id/d' | sed -e '/^#reposurgeon sourcetype/d' \
		| QUIET=$(QUIET) tapdiffer "$${test}: pathological hg loads" $${test}.fi; \
	    done) | $(TAPFILTER); \
	else echo "    Skipped, hg missing."; exit 0; \
	fi; \
	rm -f /tmp/regress-h$$$$

# Test loading from Subversion
SVN_AND_TST_FILES := $(sort $(wildcard *.svn *.tst))
SVNLOADS := $(shell echo $(SVN_AND_TST_FILES) | sed 's/\([^ ]*\)\.svn \1.tst//g; s/[^ ]*\.tst//g; s/\.svn//g')
svnload-buildregress:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	for test in $(SVNLOADS); do \
	    if [ -f $${test}.tst ] ; \
	    then \
		    echo "BUG: $${test} should have been skipped: $${test}.tst exists"; \
		    exit 1; \
		fi; \
		echo "Remaking $${test}.chk"; \
		rm -f $${test}.chk && $(MAKE) --quiet $${test}.chk \
			|| exit $(STOPOUT); \
	done

svnload-regress:
	@echo "== Testing loading of Subversion streams:"
	@$(call TAPOUT, $(SVNLOADS:%=svnload-regress-%))

$(SVNLOADS:%=svnload-regress-%): svnload-regress-%: %.svn
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; BUILDOPT=$(BUILDOPT); export BUILDOPT;\
	test=$(<:.svn=); legend=$$(sed -n '/^ ## /s///p' <"$<" 2>/dev/null || echo "(no description)"); \
	$(REPOSURGEON) "$(TESTOPT)" "$(TESTOPT)" "read <$${test}.svn" "prefer git" "write -"  >/tmp/stream$$$$ 2>&1; \
	QUIET=$(QUIET) tapdiffer "$<: $${legend}" $${test}.chk </tmp/stream$$$$; \
	if $(call AVAILABLE git) ; \
	then \
	    mkdir /tmp/reposurgeon$$$$ || exit $(STOPOUT); \
	    (cd /tmp/reposurgeon$$$$ || exit $(STOPOUT); \
		git init --quiet || exit $(STOPOUT); \
		grep --text -v 'reposurgeon:' /tmp/stream$$$$ | git fast-import --quiet) \
		    || { rm -fr /tmp/reposurgeon$$$$ /tmp/stream$$$$; exit $(STOPOUT); }; \
	    rm -fr /tmp/reposurgeon$$$$\
	else \
	    echo "    Skipped, git missing."; \
	fi; \
	rm -f /tmp/stream$$$$

# Perform full regression on SVN streams that have clean conversions -
# that is, no messages due to branch surgery or whatever.
# Empty-commit tags generated in gitspace corresponding to branch
# copies are ignored.
SVNCLEAN := \
	nut.svn \
	agito.svn \
	attrws.svn \
	authortz.svn \
	bad-author.svn \
	badattrib.svn \
	badattrib2.svn \
	binary.svn \
	blob-id.svn \
	branchreplace.svn \
	branch-drop-add.svn \
	cherry-pick.svn \
	cvstag.svn \
	debranch.svn \
	deepdirs.svn \
	delete-rename.svn \
	deletion.svn \
	delignore.svn \
	delref.svn \
	dircopyprop.svn \
	emptycommit-merge.svn \
	emptyfrom.svn \
	executable.svn \
	expunge.svn \
	filecopy.svn \
	fleetwood.svn \
	format7.svn \
	gitify.svn \
	gitignore.svn \
	ignore-blank.svn \
	lint.svn \
	mergeinfo-combine.svn \
	mergeinfo-grandparent.svn \
	mergeinfo-trunkstomp.svn \
	mergeinfo-with-split.svn \
	mergeinfo.svn \
	mixedbranch.svn \
	mkignore.svn \
	myers1.svn \
	node-kind-first.svn \
	no-forward-merge.svn \
	no-merge.svn \
	nontipcopy.svn \
	nullignores.svn \
	pangram.svn \
	permcopy1.svn \
	permcopy2.svn \
	permcopy3.svn \
	permcopy4.svn \
	replace-dir.svn \
	rootfirst.svn \
	samplebranch.svn \
	simpletag.svn \
	smallref.svn \
	snarl.svn \
	split-dir.svn \
	squash-id.svn \
	subclipse.svn \
	subdir.svn \
	svnignores.svn \
	symlink.svn \
	tagpollute.svn \
	tagsimple.svn \
	tagsimpledelete.svn \
	treecontents.svn \
	trunkstomp.svn \
	vanilla.svn \
	wrong-mergeinfo.svn \

liftcheck-regress:
	@echo "== Testing conversion correctness of Subversion streams at head revision:"
	@if  $(call AVAILABLE git) &&  $(call AVAILABLE svn); \
	then \
		$(call TAPOUT, $(SVNCLEAN:%=liftcheck-regress-%)); \
	else \
		echo "     Skipped, either git or svn is missing"; \
	fi

$(patsubst %,liftcheck-regress-%,$(wildcard *.svn)): liftcheck-regress-%: %
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; \
	liftcheck -e 'emptycommit|-root' $< || exit $(STOPOUT)

LOADS_LIFTCHECK_FULLREGRESS = nut.svn $(filter-out nut.svn,$(wildcard *.svn))
RULE_LIFTCHECK_FULLREGRESS = $(LOADS_LIFTCHECK_FULLREGRESS:%=liftcheck-regress-full-%)
liftcheck-fullregress:
	@echo "== Testing conversion correctness of all SVN revisions:"
	@if  $(call AVAILABLE git) &&  $(call AVAILABLE svn); \
	then \
		$(call TAPOUT, $(RULE_LIFTCHECK_FULLREGRESS)); \
	else \
		echo "     Skipped, either git or svn is missing"; \
	fi

$(RULE_LIFTCHECK_FULLREGRESS): liftcheck-regress-full-%: %
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; \
	liftcheck -q -r all $< || exit $(STOPOUT)

# List Subversion streams that don't convert clean without -q -r all
liftcheck-dirty:
	@for x in $(SVNCLEAN); do echo $${x} >>goodlist$$$$; done; \
	sort -o goodlist$$$$ goodlist$$$$; \
	ls *.svn | comm -23 - goodlist$$$$; \
	rm goodlist$$$$

REFLOADS=nut mergeinfo-with-split
legacy-buildregress:
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT;\
	for test in $(REFLOADS); \
	do \
	    echo "Remaking $${test}.map"; \
	    rm -f $${test}.map && $(MAKE) --quiet $${test}.map 2>/dev/null \
		|| exit $(STOPOUT); \
	done
legacy-regress:
	@echo "=== Testing legacy-map generation:"
	@REPOSURGEON=$(REPOSURGEON); export REPOSURGEON; TESTOPT="$(TESTOPT)"; export TESTOPT; \
	status=0; \
	($(call PLANLINE,$(REFLOADS)); \
	for test in $(REFLOADS); \
	do \
	    legend=$$(sed -n '/^ ## /s///p' <"$${test}.svn" 2>/dev/null || echo "(no description)"); \
	    $(REPOSURGEON) "$(TESTOPT)" "read <$${test}.svn" "legacy write -" | grep -v '^reposurgeon:' 2>/dev/null \
	    | QUIET=$(QUIT0E) tapdiffer "$${legend}" $${test}.map || status=1; \
	done; \
	exit $${status};) | $(TAPFILTER)

# Regression-testing of repomapper; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
MAPPERTESTS := $(shell ls -1 repomapper*.sh | sed '/.sh/s///')
repomapper-buildregress:
	@REPOMAPPER=$$REPOMAPPER; export REPOMAPPER; \
	for file in $(MAPPERTESTS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit $(STOPOUT); \
	done
repomapper-regress:
	@echo "=== Running repomapper tests:"
	@(REPOMAPPER=$$REPOMAPPER; export REPOMAPPER; \
	status=0; \
	$(call PLANLINE,$(MAPPERTESTS)); \
	for file in $(MAPPERTESTS); do \
	    legend=$$(sed -n '/^## /s///p' <"$${file}.sh" 2>/dev/null || echo "(no description)"); \
	    $(SHELL) $${file}.sh | QUIET=$(QUIET) tapdiffer "$${legend}" $${file}.chk || status=$(STOPOUT); \
	done; \
	exit $${status};) | $(TAPFILTER)

# Regression-testing of repotool; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
TOOLTESTS := $(shell ls -1 repotool*.sh | sed '/.sh/s///')
repotool-buildregress:
	@REPOTOOL=$$REPOTOOL; export REPOTOOL; \
	for file in $(TOOLTESTS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh --rebuild || exit $(STOPOUT); \
	done
repotool-regress:
	@echo "=== Running repotool tests:"
	@(REPOTOOL=$$REPOTOOL; export REPOTOOL; \
	status=0; \
	$(call PLANLINE,$(TOOLTESTS)); \
	for file in $(TOOLTESTS); do \
	    $(SHELL) $${file}.sh --regress || status=1; \
	done; \
	exit $${status};) | $(TAPFILTER)

# Regression-testing of repocutter; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
CUTTERTESTS := $(shell ls -1 repocutter*.sh | sed '/.sh/s///')
repocutter-buildregress:
	@REPOMAPPER=$$REPOMAPPER; export REPOMAPPER; \
	for file in $(CUTTERTESTS); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh >$${file}.chk || exit $(STOPOUT); \
	done
repocutter-regress:
	@echo "=== Running repocutter tests:"
	@(REPOCUTTER=$$REPOCUTTER; export REPOCUTTER; \
	status=0; \
	$(call PLANLINE,$(CUTTERTESTS)); \
	for file in $(CUTTERTESTS); do \
	    legend=$$(sed -n '/^## /s///p' <"$${file}.sh" 2>/dev/null || echo "(no description)"); \
	    $(SHELL) $${file}.sh | QUIET=$(QUIET) tapdiffer "$${legend}" $${file}.chk || status=$(STOPOUT); \
	done; \
	exit $${status};) | $(TAPFILTER)

# Regression-testing of Subversion permission-bit and other exceptional cases; look at the *.sh and
# corresponding *.chk files to see which tests this runs.
SVNCHECK := $(shell ls -1 svncheck*.sh | sed '/.sh/s///')
svncheck-buildregress:
	@for file in $(SVNCHECK); do \
	    echo "Remaking $${file}.chk"; \
	    $(SHELL) $${file}.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set testmode" 'read -' 'prefer git' 'write -' >$${file}.chk || exit $(STOPOUT); \
	done
svncheck-regress:
	@echo "=== Running svncheck tests:"
	@status=0; \
	if $(call AVAILABLE svn) ; \
	then \
	    ($(call PLANLINE,$(SVNCHECK)); \
	    for file in $(SVNCHECK); do \
		legend=$$(sed -n '/^## /s///p' <"$${file}.sh" 2>/dev/null || echo "(no description)"); \
		$(SHELL) $${file}.sh -d | $(REPOSURGEON) "$(TESTOPT)" "set testmode" 'read -' 'prefer git' 'write -' | tapdiffer "$${legend}" $${file}.chk || status=1; \
	    done;) | $(TAPFILTER); \
	else echo "    Skipped, svn missing."; exit 0; \
	fi; \
	exit $${status};

SPORADIC = hashcheck.sh workflow-cvs-git.sh workflow-svn-git.sh
sporadic:
	@echo "=== Sporadic tests:"
	@($(call PLANLINE,$(SPORADIC)); \
	for file in $(SPORADIC); \
	do \
		sh $$file || exit $(STATUS); \
	done) | $(TAPFILTER)

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
	@cd test-checkout || exit $(STOPOUT); set -x; \
		svn mkdir trunk || exit $(STOPOUT); \
		svn mkdir tags || exit $(STOPOUT); \
		svn mkdir branches || exit $(STOPOUT); \
		echo "Directory layout." | svn commit -F - || exit $(STOPOUT); \
		echo "This is a test Subversion repository" >trunk/README; \
		svn add trunk/README || exit $(STOPOUT); \
		echo "Initial README content." | svn commit -F - || exit $(STOPOUT)
svn-dump:
	@svnadmin dump test-repo 

# end
