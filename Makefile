#
# makefile for reposurgeon
#
INSTALL=install
prefix?=/usr/local
mandir?=share/man
target=$(DESTDIR)$(prefix)

META = README.adoc INSTALL.adoc NEWS.adoc
PAGES = reposurgeon.adoc repocutter.adoc repomapper.adoc repotool.adoc repobench.adoc
DOCS = $(PAGES) repository-editing.adoc oops.svg
SOURCES = $(shell ls */*.go) repobench reposurgeon-mode.el go.mod go.sum extractversion.sh
SOURCES += Makefile control reposturgeon.png reposurgeon-git-aliases
SOURCES += Dockerfile ci/prepare.sh .gitlab-ci.yml
SOURCES += $(META) $(DOCS) COPYING

.PHONY: all build install uninstall version check release refresh \
	docker-build docker-check docker-check-noscm get test fmt lint

# Conditionalize building of documentation on whether our formatter is installed
# Building the documentation also requires awk and ruby.
ifneq (, $(shell which asciidoctor))
HTMLFILES = $(DOCS:.adoc=.html) $(META:.adoc=.html)
endif

BINARIES  = reposurgeon repotool repomapper repocutter
INSTALLABLES = $(BINARIES) repobench
MANPAGES  = $(PAGES:.adoc=.1)
SHARED    = $(META) reposurgeon-git-aliases $(HTMLFILES) COPYING

.PHONY: all fullinstall build stable-golang current-golang helpers test-helpers \
		docincludes get test lint fmt clean install uninstall dist version \
		check fixme dist docker-build docker-check docker-check-scm release \
		refresh

awk_supports_posix_arg = $(shell awk --posix "" >/dev/null 2>&1; echo $$?)
ifeq ($(awk_supports_posix_arg), 0)
AWK = awk --posix
else
AWK = awk
endif

# Binaries need to be built before generated documentation parts can be made.
all: build cuttercommands.inc toolcommands.inc $(MANPAGES) $(HTMLFILES)

GOFLAGS=-ldflags='-X main.version=$(VERS)'
# The following would produce reproducible builds, but it breaks Gitlab CI.
#GOFLAGS=-gcflags 'all=-N -l -trimpath $(GOPATH)/src' -asmflags 'all=-trimpath $(GOPATH)/src'
# The following could be used for escape analysis
#GOFLAGS+="-gcflags=-m"
build: surgeon/help-index.go
	-test -f go.mod || (go mod init && go get)
	go build $(GOFLAGS) -o repocutter ./cutter
	go build $(GOFLAGS) -o repomapper ./mapper
	go build $(GOFLAGS) -o reposurgeon ./surgeon
	go build $(GOFLAGS) -o repotool ./tool

reposurgeon: build

#
# Fast installation in apt-world. Fire either stable-golang or current-golang,
# then helpers, then test-helpers if you want to run the test suite,
#
fullinstall: stable-golang helpers test-helpers

stable-golang:
	sudo apt-get -y install golang

# This may improve performance, if the backports repository
# has been updated to a version more recemnt than your distro's.
current-golang:
	sudo add-apt-repository ppa:longsleep/golang-backports
	sudo apt update
	sudo apt install golang-go
	go version

# As of Ubuntu 20.04 there is no longer a package names "awk".
helpers:
	command -v asciidoctor >/dev/null 2>&1 || sudo apt-get -y install asciidoctor
	command -v awk >/dev/null 2>&1 || sudo apt-get -y install gawk

test-helpers:
	sudo apt-get -y install cvs-fast-export subversion cvs mercurial rsync golint shellcheck

#
# Documentation
#

# Note: to suppress the footers with timestamps being generated in HTML,
# we use "-a nofooter".
# To debug asciidoc problems, you may need to run "xmllint --nonet --noout --valid"
# on the intermediate XML that throws an error.
.SUFFIXES: .html .adoc .1

.adoc.1:
	asciidoctor -D. -a nofooter -b manpage $<
.adoc.html:
	asciidoctor -D. -a webfonts! $<

# This is a list of help topics for which the help is in regular format
# and there is no additional material included in the long-form manual only.
# This means there's one line of BNF, a blank separator line, and one or
# more blank-line-separated paragraphs of running text.
BNF_TOPICS = \
	add \
	append \
	authors \
	assign \
	branchlift \
	changelogs \
	checkout \
	checkpoint \
	choose \
	clear \
	clone \
	coalesce \
	count \
	create \
	debranch \
	dedup \
	define \
	delete \
	diff \
	divide \
	do \
	drop \
	exit \
	filter \
	gc \
	gitify \
	graft \
	graph \
	hash \
	help \
	history \
	ignores \
	incorporate \
	legacy \
	lint \
	list \
	log \
	merge \
	move \
	msgin \
	msgout \
	operators \
	prefer \
	prepend \
	preserve \
	print \
	quit \
	rebuild \
	remove \
	rename \
	renumber \
	reorder \
	resolve \
	set \
	setfield \
	setperm \
	shell \
	show \
	sourcetype \
	split \
	stampify \
	strip \
	tagify \
	timeoffset \
	timequake \
	transcode \
	unassign \
	undefine \
	unite \
	unmerge \
	unpreserve \
	version \
	view
UNANCHORED_TOPICS = \
	functions \
	options \
	redirection \
	regexp \
	selection \
	syntax
TOPICS = $(BNF_TOPICS) $(UNANCHORED_TOPICS)
# These are in regular form, but the entries in the
# long-form manual have additional material.
SHORTFORM = \
	read \
	squash \
	write
# These are all the non-regular topics
EXCEPTIONS = \
	attribution \
	profile \
	reparent
# Most of the command descriptions in Repository editing are reposurgeon's embedded
# help, lightly massaged into asciidoc format. This is how to generate them.
docincludes: surgeon/reposurgeon.go reposurgeon repository-editing.adoc
	@rm -fr docinclude; mkdir docinclude
	@get_help() { \
		./reposurgeon "set flag asciidoc" "help $${1}" | awk -f poundsign.awk; \
	}; \
	for topic in $(BNF_TOPICS); \
	do \
		echo "[[$${topic}_cmd,$${topic}]]" >>"docinclude/$${topic}.adoc"; \
		get_help "$${topic}" >>"docinclude/$${topic}.adoc"; \
	done; \
	for topic in $(UNANCHORED_TOPICS); \
	do \
		get_help "$${topic}" >>"docinclude/$${topic}.adoc"; \
	done;
	@./reposurgeon "help options" | sed '/:/s//::/' >docinclude/options.adoc

repository-editing.html: docincludes
	@./repository-editing.rb
repository-editing.pdf: docincludes
	@asciidoctor-pdf repository-editing.adoc

# Audit for embedded-help entries not used as inclusions (column 1)
# or inclusions for which there are no corresponding help topics (column 2).
# Column 2 should always be empty.
helpcheck:
	@(for topic in $(TOPICS); do echo $${topic}; done) | sort >/tmp/topics$$$$; \
	sed -n <repository-editing.adoc '/include::docinclude\/\([a-z]*\).adoc\[\]/s//\1/p' | sort >/tmp/includes$$$$ ; \
	comm -3 /tmp/topics$$$$ /tmp/includes$$$$; \
	rm /tmp/topics$$$$ /tmp/includes$$$$

# Report most grammar summary lines. Missing: the SHORTFORM and EXCEPTION topics.
summary:
	@for topic in $(BNF_TOPICS); do head -2 "docinclude/$${topic}.adoc"; done | grep -v '^\[\[' | sed '/::$$/s///'

surgeon/help-index.go: help-index.awk repository-editing.adoc
	$(AWK) -f $^ >$@

cuttercommands.inc: build
	./repocutter -q docgen >cuttercommands.inc

toolcommands.inc: build
	./repotool docgen >toolcommands.inc

#
# Auxiliary Go tooling productions
#

get:
	go get -u ./...	# go get -u=patch for patch releases

test:
	go test $(TESTOPTS) ./surgeon
	go test $(TESTOPTS) ./cutter

lint:
	golint -set_exit_status ./...
	shellcheck -f gcc repobench test/fi-to-fi test/liftcheck test/singlelift test/svn-to-git test/svn-to-svn test/delver test/*.sh test/*test test/mvtest

fmt:
	gofmt -s -w .

#
# Cleaning
#
clean:
	rm -f $(BINARIES)
	rm -fr docinclude cuttercommands.inc toolcommands.inc *~ *.1 *.html *.tar.xz MANIFEST *.md5
	rm -fr .rs .rs* test/.rs test/.rs* covdatafiles merged-coverage profile.txt profile.html
	rm -f typescript test/typescript

#
# Installation
#
# Note that this does not run a build, so it will error out (harmlessly) if you 
# have not done "make all" or "make build" first.  There's a conflict between
# Go's preference for Rebuilding All The Things and the traditional makefile
# attempt to do as little build work as possible at any given time.
#
install:
	$(INSTALL) -d "$(target)/bin"
	$(INSTALL) -d "$(target)/share/doc/reposurgeon"
	$(INSTALL) -d "$(target)/$(mandir)/man1"
	$(INSTALL) -m 755 $(INSTALLABLES) "$(target)/bin"
	$(INSTALL) -m 644 $(SHARED) "$(target)/share/doc/reposurgeon"
	$(INSTALL) -m 644 $(MANPAGES) "$(target)/$(mandir)/man1"

#
# Uninstallation
#

INSTALLED_BINARIES := $(INSTALLABLES:%="$(target)/bin/%")
INSTALLED_SHARED   := $(SHARED:%="$(target)/share/doc/reposurgeon/%")
INSTALLED_MANPAGES := $(MANPAGES:%="$(target)/$(mandir)/man1/%")

uninstall:
	rm -f $(INSTALLED_BINARIES)
	rm -f $(INSTALLED_MANPAGES)
	rm -f $(INSTALLED_SHARED)
	rmdir "$(target)/share/doc/reposurgeon"

VERS=$(shell sed -n <NEWS.adoc '/^[0-9]/s/:.*//p' | head -1)

version:
	@echo $(VERS)

#
# Code validation
#
# See also:
# https://goreportcard.com/report/gitlab.com/esr/reposurgeon

check: lint all test
	$(MAKE) -C test --quiet check BINDIR=$(realpath $(CURDIR))

fixme:
	@if command -v rg; then \
		rg --no-heading FIX''ME; \
	else \
		find . -type f -exec grep -n FIX''ME {} /dev/null \; | grep -v "[.]git"; \
	fi

#
# Coverage testing
#
# See also:
# https://go.dev/testing/coverage/
# https://go.dev/blog/integration-test-coverage
# https://github.com/dave/courtney
#
# Requires Go 1.20.

coverage:
	rm -rf covdatafiles
	mkdir covdatafiles
	make GOFLAGS=-cover GOCOVERDIR=$(realpath $(CURDIR))/covdatafiles check
	rm -rf merged-coverage ; mkdir merged-coverage ; go tool covdata merge -i=covdatafiles -o=merged-coverage
	rm -rf covdatafiles
	go tool covdata textfmt -i=merged-coverage -o=profile.txt
	go tool cover -html=profile.txt -o profile.html

#
# Continuous integration.  More specifics are in the ci/ directory
#

docker-build: $(SOURCES)
	docker build -t reposurgeon .

docker-check: docker-build
	docker run --rm -i -e "MAKEFLAGS=$(MAKEFLAGS)" -e "MAKEOVERRIDES=$(MAKEOVERRIDES)" reposurgeon make check

docker-check-only-%: docker-build
	docker run --rm -i -e "MAKEFLAGS=$(MAKEFLAGS)" -e "MAKEOVERRIDES=$(MAKEOVERRIDES)" reposurgeon bash -c "make -C ci install-only-$(*) && make check"

docker-check-no-%: docker-build
	docker run --rm -i -e "MAKEFLAGS=$(MAKEFLAGS)" -e "MAKEOVERRIDES=$(MAKEOVERRIDES)" reposurgeon bash -c "make -C ci install-no-$(*) && make check"

# Test that support for each VCS stands on its own and test without legacy
# VCS installed
docker-check-noscm: docker-check-only-bzr docker-check-only-cvs \
    docker-check-only-git docker-check-only-mercurial \
    docker-check-only-subversion docker-check-no-cvs 
# Due to many tests depending on git, docker-check-only-mercurial is a very poor
# test of Mercurial

#
# Release shipping.
#

# Don't try using tar --transform, it tries to get too clever with symlinks 
reposurgeon-$(VERS).tar.xz: $(SHIPPABLE)
	(git ls-files; ls *.1) | sed s:^:reposurgeon-$(VERS)/: >MANIFEST
	(cd ..; ln -s reposurgeon reposurgeon-$(VERS))
	(cd ..; tar -cJf reposurgeon/reposurgeon-$(VERS).tar.xz `cat reposurgeon/MANIFEST`)
	(cd ..; rm reposurgeon-$(VERS) reposurgeon/MANIFEST)

dist: reposurgeon-$(VERS).tar.xz

reposurgeon-$(VERS).md5: reposurgeon-$(VERS).tar.xz
	@md5sum reposurgeon-$(VERS).tar.xz >reposurgeon-$(VERS).md5

release: reposurgeon-$(VERS).tar.xz reposurgeon-$(VERS).md5 $(HTMLFILES)
	shipper version=$(VERS) | sh -e -x

refresh: $(HTMLFILES)
	shipper -N -w version=$(VERS) | sh -e -x

# end
