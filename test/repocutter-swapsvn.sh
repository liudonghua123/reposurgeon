#!/bin/sh
## Test structural path-element swapping
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapsvn <multigen.svn 2>&1 | repocutter -q  -t "$(basename $0)" see 2>&1

