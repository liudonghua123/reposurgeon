#!/bin/sh
## Test expensive copy with repocutter
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" select <vanilla.svn

