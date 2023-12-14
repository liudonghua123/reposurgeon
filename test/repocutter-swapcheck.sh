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
    vc init svn

    vc mkdir crossflight            # This is OK
    vc mkdir crossflight/src        # This should be reported

    vc mkdir toodeep                # Would be OK if not for standard layout buried underneath
    vc mkdir toodeep/proj           # Should be reported
    vc mkdir toodeep/proj/trunk     # trunk is one level too deep, should be reported
    vc mkdir toodeep/proj/tags      # tags is one level too deep, should be reported

    vc mkdir trunk                  # Should not be reported
    vc mkdir tags                   # Should not be reported
    vc mkdir branches               # Should not be reported

    vc wrap
} >"${msgsink}" 2>&1

# shellcheck disable=SC2086
vc export "swapcheck test load" | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapcheck 2>&1










