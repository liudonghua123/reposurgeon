#!/bin/sh
## Test structural path-element swapping
${REPOCUTTER:-repocutter} -q swapsvn <multigen.svn | repocutter -q see

