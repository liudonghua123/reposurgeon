#!/bin/sh
## Test path rename (with restriction)
# Note, the stream this produces is invalid.
# The goal ios to demonstate that path transforms
# are only done on selrcted revisions.
${REPOCUTTER:-repocutter} -r 3:5 -q pathrename README WOBBLE WOBBLE WIBBLE <vanilla.svn

