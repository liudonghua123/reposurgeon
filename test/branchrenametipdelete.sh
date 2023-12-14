#!/bin/sh
# Generate a Subversion output stream with a deleted branch (that also contains spaces)
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
	o) outsink=/tmp/genout$$; target="${OPTARG}";;
	v) msgsink=/dev/stdout; outsink=/dev/null;;
	*) echo "$0: unknown flag $opt" >&2; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

here=$(pwd)
{
    vc init svn
    vc stdlayout
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

    vc wrap
} >"${msgsink}" 2>&1
vc export "branch with spaces deletion example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
