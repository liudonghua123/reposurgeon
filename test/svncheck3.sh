#!/bin/sh
## Test propagation of executable bit by directory copy, second variant
# Originally made from gen-dump2.sh, attached to issue #103.
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
	*) echo "not ok - $0: unknown flag $opt"; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))
{
    repository init svn
    mkdir trunk
    svn add trunk
    svn commit -m "Create trunk."
    tapcd trunk
    mkdir dir1
    echo "file" > dir1/file
    svn add dir1
    svn commit -m "Create dir1/file."
    svn propset svn:executable '*' dir1/file
    svn commit -m "Make dir1/file executable."
    svn up
    svn cp dir1 dir2
    svn commit -m "Copy dir1 to dir2."
    if [ -x trunk/dir2/file ]; then executable=yes; fi
    repository wrap
} >/dev/$verbose 2>&1
# shellcheck disable=SC2010
if [ "$dump" = yes ]
then
    repository export "exec propagation test"
elif [ "${executable}" = yes ]
then
    echo "ok - $0: executable permission is as expected"
    exit 0
else
    echo "not ok - $0: executable permission was expected"
    exit 1
fi

# end
