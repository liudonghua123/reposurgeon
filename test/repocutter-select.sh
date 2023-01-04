#!/bin/sh
## Test selection
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 0:4 select <vanilla.svn

