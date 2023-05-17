#!/bin/sh
## Test strip subcommand to skeletonize a repo
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -h sha256 <vanilla.svn hash 2>&1
