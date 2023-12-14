#!/bin/sh
# This demonstrates the behavior descrebed in  
# https://gitlab.com/esr/reposurgeon/-/issues/357
#
# The sequences of operations is this:
#
# 1-1   add      branches/
# 1-2   add      tags/
# 1-3   add      trunk/
# 2-1   add      trunk/file
# 3-1   change   trunk/file
# 4-1   add      branches/release-1.0/
# 5-1   copy     branches/release-1.0/file from 4:trunk/file
# 6-1   change   branches/release-1.0/file
# 7-1   change   trunk/file
#
# If this isn't turned into a branch creation something
# has gone badly wrong.
#
# In an ideal world, we might want the gitspace commit
# corresponding to r5 to become a tag.
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
    tapcd ..

    # r2
    echo "initial content" >trunk/file
    svn add trunk/file
    svn commit -m "add initial content"

    # r3
    echo "more content" >>trunk/file
    svn commit -m "continue development"

    # r4
    mkdir -p branches/release-1.0
    svn add branches/release-1.0
    svn commit -m "prepare empty release branch"
    svn up

    # r5
    svn copy trunk/* branches/release-1.0
    svn commit -m "copy everything from trunk to release branch"
    svn up

    # r6
    echo "even more branch content" >>branches/release-1.0/file
    svn commit -m "continue development on branch"

    # r7
    echo "even more trunk content" >>trunk/file
    svn commit -m "continue trunk development"

    repository wrap
} >"${msgsink}" 2>&1
repository export "branch creation via copy-to-empty-dir example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
