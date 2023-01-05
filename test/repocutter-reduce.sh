#!/bin/sh
## Test reduce subcommand to topologically reduce a repo
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" reduce <debranch.svn 2>&1 
# FIXME: Repair the repocutter-reduce testload rather than ignoring the missing revision
exit 0

