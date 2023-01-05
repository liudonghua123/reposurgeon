#!/bin/sh
## Test renumbering and patching of copyfrom revisions
# shellcheck disable=SC2086
(${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 0:5,7:17 select <branchreplace.svn | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" renumber) 2>&1
# FIXME: Repair the repocutter-renumber testload rather than ignoring the missing revision
exit 0

