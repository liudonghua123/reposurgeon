#! /bin/sh
## Test repocutter propset
# Output should reveal alteration of the propset line

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare vanilla.svn -r 5.1 propset foo=qux

