#!/bin/sh
## Test testification
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" testify <simpletag.svn



