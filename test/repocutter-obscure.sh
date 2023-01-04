#!/bin/sh
## Test obscuring of filenames
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" obscure <nut.svn

