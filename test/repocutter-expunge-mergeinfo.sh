#!/bin/sh
## Test patching of mergeinfo references when expunging
trap 'rm -fr /tmp/mergeinfo$$.see' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <mergeinfo.svn >/tmp/mergeinfo$$.see
${REPOCUTTER:-repocutter} -q -r 5:6 expunge VERSION src <mergeinfo.svn | ${REPOCUTTER:-repocutter} -q see | diff -u --label A --label B /tmp/mergeinfo$$.see -
exit 0

