#!/bin/sh
## Test log subcommand
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" log <vanilla.svn 2>&1
