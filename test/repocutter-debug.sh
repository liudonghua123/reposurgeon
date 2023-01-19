#!/bin/sh
## Test repocutter debug subcommand
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -r 2:4 -t "$(basename $0)" debug 1 2>&1 <vanilla.svn

