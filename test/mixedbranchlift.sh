#!/bin/sh
# Generate a Subversion output stream for testing branchlift with mixed commits
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

rm -f /tmp/genout$$
outsink=/dev/stdout
msgsink=/dev/null
while getopts o:v opt
do
    case $opt in
	o) outsink=/tmp/genout$$; target=${OPTARG};;
	v) msgsink=/dev/stdout; outsink=/dev/null;;
	*) echo "$0: unknown flag $opt" >&2; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

here=$(pwd)
{
    repository init svn
    repository stdlayout

    # r2
    svn mkdir nonbranch1
    echo foo >nonbranch1/README
    svn add nonbranch1/README
    svn commit -m 'add nonbranch1/README'
    svn up

    # r3
    svn mkdir nonbranch2
    echo liquid >nonbranch2/DRINKME
    svn add nonbranch2/DRINKME
    svn commit -m 'add nonbranch2/DRINKME'
    svn up

    # r4
    echo bar >> nonbranch1/README
    svn commit -m 'nonbranch1/README: add bar'
    svn up

    # r5 - mixed commit
    echo end >> nonbranch1/README
    echo sky >> nonbranch2/DRINKME
    svn commit -m 'nonbranch1/README: add end & nonbranch2: add sky'
    svn up

    # r6
    echo falling >nonbranch2/DRINKME
    svn commit -m 'append to nonbranch2/DRINKME'
    svn up

    repository wrap
} >"${msgsink}" 2>&1
repository export "Example of mixed-directory commits on master for testing branchlift" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
