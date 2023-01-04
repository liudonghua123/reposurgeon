#!/bin/sh
## Test replace subcommand to replace text in blobs

# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" <vanilla.svn replace "/of modified/of re-modified/"
