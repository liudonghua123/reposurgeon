#!/bin/sh
## Test propagation of executable bit by directory copy
# This was made from gen-dump.h. attached to issue #103.
#
# This is not a generator.

# shellcheck disable=SC1091
. ./common-setup.sh

dump=no
verbose=null
while getopts dv opt
do
    case $opt in
	d) dump=yes;;
	v) verbose=stdout;;
	*) echo "not ok - $0: unknown flag $opt" >&2; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))
{
    vc init svn
    vc stdlayout
    svn mkdir dir1
    echo "file" > dir1/file
    svn add dir1/file
    svn commit -m "Create dir1/file."
    svn propset svn:executable '*' dir1/file
    svn commit -m "Make dir1/file executable."
    svn up
    ls -l dir1/file
    svn cp dir1 dir2
    svn commit -m "Copy dir1 to dir2."
    svn up
    ls -l dir1/file dir2/file
    if [ -x dir2/file ]; then executable=yes; fi
    vc wrap
} >/dev/$verbose 2>&1
# shellcheck disable=SC2010
if [ "$dump" = yes ]
then
    vc export "exec propagation test"
elif [ "${executable}" = yes ]
then
    echo "ok - $0: executable permission is as expected"
    exit 0
else
    echo "not ok $0: executable permission was expected"
    exit 1
fi

# end
