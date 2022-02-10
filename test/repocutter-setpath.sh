#! /bin/sh
## Test repocutter setpath
# Output should reveal alteration of the node path

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare simpletag.svn -r 7.1 setpath arglebargle

