#! /bin/sh
## Test repocutter propdel
# Output should reveal deletion of the propset line

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare vanilla.svn propdel foo

