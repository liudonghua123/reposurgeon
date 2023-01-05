#!/bin/sh
## Test path rename (with restriction)
# Note, the stream this produces is invalid.
# The goal is to demonstrate that path transforms
# are only done on selected revisions.
# The output file should not contain XX
# because WI is not a segment match.
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -r 3:5 -q -t "$(basename $0)" pathrename README WOBBLE WOBBLE WIBBLE WI XX <vanilla.svn 2>&1

