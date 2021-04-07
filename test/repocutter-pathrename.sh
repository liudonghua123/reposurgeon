#!/bin/sh
## Test path rename
${REPOCUTTER:-repocutter} -q pathrename README WOBBLE WOBBLE WIBBLE <vanilla.svn

