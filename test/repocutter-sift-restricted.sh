#!/bin/sh
## Test path sift with range restriction
trap 'rm -fr /tmp/expunge$$.see' EXIT HUP INT QUIT TERM
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see <expunge.svn >/tmp/expunge$$.see
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 2:3 sift README <expunge.svn | ${REPOCUTTER:-repocutter} -q see | diff -u --label A --label B /tmp/expunge$$.see -
exit 0
