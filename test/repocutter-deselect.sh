#!/bin/sh
## Test deselection
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 4:HEAD deselect <vanilla.svn

