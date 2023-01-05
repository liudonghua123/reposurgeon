#!/bin/sh
## Test patching of mergeinfo references when expunging
trap 'rm -fr /tmp/mergeinfo$$.see' EXIT HUP INT QUIT TERM
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see <mergeinfo.svn >/tmp/mergeinfo$$.see 2>&1
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 5:6 expunge VERSION src <mergeinfo.svn 2>&1 | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see 2>&1 | diff -u --label A --label B /tmp/mergeinfo$$.see -
exit 0

