#! /bin/sh
## Test repocutter propset
# Output should reveal alteration of the propset line

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare -r 5.1 propset foo=qux <vanilla.svn

