#!/bin/sh
# Generate an SVN stream which may provoke reposurgeon to create a duplicate 'refs/tags/release-1.0-root'
# This used to lift to an invalid input stream, see https://gitlab.com/esr/reposurgeon/-/issues/355
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
    tapcd ..

    # r2
    echo foo >trunk/file
    svn add trunk/file
    svn commit -m 'add file'

    # r3
    svn copy ^/trunk ^/branches/release-1.0 -m "Create release branch 1.0"
    svn up

    # r4
    echo bar >>branches/release-1.0/file
    svn commit -m 'Prepare release 1.0'

    # r5
    svn copy ^/branches/release-1.0 ^/tags/release-1.0 -m "Tag release 1.0"
    svn up

    # r6
    svn up
    echo bar >>tags/release-1.0/file
    svn commit -m 'Oops, forgot something! (this turns the "tag" back into a "branch")'

    repository wrap
} >"/dev/${verbose}" 2>&1

# shellcheck disable=2010
if [ "$dump" = yes ]
then
    repository export "tag with commit after creation example"
fi

# end
