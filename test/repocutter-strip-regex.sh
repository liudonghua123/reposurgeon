#!/bin/sh
## Test strip subcommand to skeletonize selected paths
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" <vanilla-2file.svn strip file1
