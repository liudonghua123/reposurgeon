#! /bin/sh
## Test repocutter setcopyfrom
# Output should reveal alteration of the copyfrom path

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare -r 7.1 setcopyfrom arglebargle <simpletag.svn

