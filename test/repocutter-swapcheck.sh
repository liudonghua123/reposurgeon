#!/bin/sh
## Test repocutter swapcheck
#
# This is not a generator.

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

msgsink=/dev/null
while getopts v opt
do
    case $opt in
	v) msgsink=/dev/stdout;;
	*) echo "$0: unknown flag $opt" >&2; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

{
    repository init svn

    repository mkdir crossflight            # This is OK
    repository mkdir crossflight/src        # This should be reported

    repository mkdir toodeep                # Would be OK if not for standard layout buried underneath
    repository mkdir toodeep/proj           # Should be reported
    repository mkdir toodeep/proj/trunk     # trunk is one level too deep, should be reported
    repository mkdir toodeep/proj/tags      # tags is one level too deep, should be reported

    repository mkdir trunk                  # Should not be reported
    repository mkdir tags                   # Should not be reported
    repository mkdir branches               # Should not be reported

    repository wrap
} >"${msgsink}" 2>&1

# shellcheck disable=SC2086
repository export "swapcheck test load" | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapcheck 2>&1










