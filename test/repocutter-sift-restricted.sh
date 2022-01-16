#!/bin/sh
## Test path sift with range restriction
trap 'rm -fr /tmp/expunge$$.see' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <expunge.svn >/tmp/expunge$$.see
${REPOCUTTER:-repocutter} -q -r 2:3 sift README <expunge.svn | ${REPOCUTTER:-repocutter} -q see | diff -u --label A --label B /tmp/expunge$$.see -
exit 0
