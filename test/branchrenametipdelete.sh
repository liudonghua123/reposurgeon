#!/bin/sh
# Generate a Subversion output stream with a deleted branch (that also contains spaces)
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

dump=no
verbose=null
while getopts dv opt
do
    case $opt in
	d) dump=yes;;
	v) verbose=stdout;;
	*) echo "not ok - $0: unknown flag $opt"; exit 1;;
    esac
done

# shellcheck disable=SC2004
shift $(($OPTIND - 1))
{
    repository init svn
    repository stdlayout
    tapcd ..

    # r2
    echo "initial content" >trunk/file
    svn add trunk/file
    svn commit -m "add initial content"

    # r3
    echo "more content" >>trunk/file
    svn commit -m "continue development"

    # r4
    svn copy "trunk" "branches/first-branch"
    svn commit -m "copy trunk to first new branch"
    svn up

    # r5
    echo "even more branch content" >>"branches/first-branch/file"
    svn commit -m "continue development on first branch"
    svn up

    # r6
    svn rm "branches/first-branch"
    svn commit -m "delete first branch"
    svn up

    # r7
    svn copy "trunk" "branches/second-branch"
    svn commit -m "copy trunk to new branch"
    svn up

    # r8
    echo "even more branch content" >>"branches/second-branch/file"
    svn commit -m "continue development on branch"

    # r9
    echo "even more trunk content" >>trunk/file
    svn commit -m "continue trunk development"

    repository wrap
} >"/dev/${verbose}" 2>&1

# shellcheck disable=2010
if [ "$dump" = yes ]
then
    repository export "branch with spaces deletion example"
fi

# end
