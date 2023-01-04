#!/bin/sh
## Test structural path-element swapping
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapsvn <multigen.svn | repocutter -q see

