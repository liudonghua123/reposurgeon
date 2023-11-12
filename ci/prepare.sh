#!/bin/bash

apt-get update -qy && apt-get install -qy --no-install-recommends \
			      asciidoctor \
			      brz \
			      bzr \
			      cvs \
			      cvs-fast-export \
			      darcs \
			      golang \
			      golint \
			      mercurial \
			      rsync \
			      shellcheck \
			      subversion \
			      time \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*
type go
go version

echo
echo ============= Dependency install complete =============
echo USER=$USER PWD=$PWD
echo
