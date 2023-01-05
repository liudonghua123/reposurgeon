#!/bin/sh
## Test path-set closure operation
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" closure branches/import 2>&1 <cvstag.svn 
