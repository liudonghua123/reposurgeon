#!/bin/sh
## Test path sifting

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare sift dev <expunge.svn 

