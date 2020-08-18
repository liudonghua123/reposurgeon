#!/bin/sh
## Test replace subcommand to replace text in blobs
${REPOCUTTER:-repocutter} -q <vanilla.svn replace "/of modified/of re-modified/"
