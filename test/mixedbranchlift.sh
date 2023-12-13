#!/bin/sh
# Generate a Subversion output stream for testing branchlift with mixed commits
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

dump=yes
verbose=null
while getopts v opt
do
    case $opt in
	v) verbose=stdout; dump=no;;
	*) echo "not ok - $0: unknown flag $opt"; exit 1;;
    esac
done

# shellcheck disable=SC2004
shift $(($OPTIND - 1))
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
} >"/dev/${verbose}" 2>&1

# shellcheck disable=2010
if [ "$dump" = yes ]
then
    repository export "Example of mixed-directory commits on master for testing branchlift"
fi

# end
