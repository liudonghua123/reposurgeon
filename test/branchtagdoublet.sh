#!/bin/sh
# Generate an SVN stream which may provoke reposurgeon to create a duplicate 'refs/tags/release-1.0-root'
# This used to lift to an invalid input stream, see https://gitlab.com/esr/reposurgeon/-/issues/355
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
} >"${msgsink}" 2>&1
repository export "tag with commit after creation example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
