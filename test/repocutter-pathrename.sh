#!/bin/sh
## Test path rename (with restriction)
# Note, the stream this produces is invalid.
# The goal ios to demonstate that path transforms
# are only done on selrcted revisions.
# The output file should not contain XX
# because WI is not a segment match.
${REPOCUTTER:-repocutter} -r 3:5 -q pathrename README WOBBLE WOBBLE WIBBLE WI XX <vanilla.svn

