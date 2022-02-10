#! /bin/sh
## Test repocutter proprenaname
# Output should reveal alteration of the proprenaname line

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare vanilla.svn proprename 'foo->wibble'

