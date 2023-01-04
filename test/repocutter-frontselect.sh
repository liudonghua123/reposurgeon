#!/bin/sh
## Test front selection
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 0:3 select <vanilla.svn

