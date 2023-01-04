#!/bin/sh
## Test repocutter count command
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" count <vanilla.svn



