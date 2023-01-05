#!/bin/sh
## Test path-element swapping
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swap <swap.svn 2>&1

