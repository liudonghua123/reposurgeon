#!/bin/sh
## Test renumbering and patching of copyfrom revisions
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 0:5,7:17 select <branchreplace.svn | ${REPOCUTTER:-repocutter} -q renumber


