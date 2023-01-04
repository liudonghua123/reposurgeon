#!/bin/sh
## Test patching of mergeinfo references when expunging
trap 'rm -fr /tmp/mergeinfo$$.see' EXIT HUP INT QUIT TERM
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see <mergeinfo.svn >/tmp/mergeinfo$$.see
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 5:6 expunge VERSION src <mergeinfo.svn | ${REPOCUTTER:-repocutter} -q see | diff -u --label A --label B /tmp/mergeinfo$$.see -
exit 0

