#!/bin/sh
## Test path expunge with range restriction
${REPOCUTTER:-repocutter} -q -r 3 expunge 'README' <simpletag.svn
