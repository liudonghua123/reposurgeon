#!/bin/sh
# Generate a Subversion output stream with a "clean" tag (1.0) and one that was committed to after tagging (2.0).
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
    echo foo >trunk/file
    svn add trunk/file
    svn commit -m 'add file'

    # r3
    svn copy ^/trunk ^/tags/1.0 -m "Tag Release 1.0"

    # r4
    svn copy ^/trunk ^/tags/2.0 -m "Tag Release 2.0"

    # r5
    svn up
    echo bar >>tags/2.0/file
    svn commit -m 'Commit to Release 2.0 after tagging'

    vc wrap
} >"${msgsink}" 2>&1
vc export "tag with commit after creation example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi
  
# end
