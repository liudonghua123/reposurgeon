#!/bin/sh
## Test path-set closure operation
${REPOCUTTER:-repocutter} -q closure branches/import <cvstag.svn 
