#!/bin/sh
## Test path sift with range restriction
# Note that we're ignboring repocutter stderr output in the second test.
trap 'rm -fr /tmp/expunge$$.see' EXIT HUP INT QUIT TERM
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see <expunge.svn >/tmp/expunge$$.see 2>&1
# shellcheck disable=SC2086
(${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 2:3 sift README <expunge.svn | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see) 2>/dev/null | diff -u --label A --label B /tmp/expunge$$.see -
exit 0
