#!/bin/sh
## Test path expunge with range restriction
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 3 expunge 'README' <simpletag.svn 2>&1
exit 0
