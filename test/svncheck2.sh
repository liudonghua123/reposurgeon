#!/bin/sh
## Test propagation of executable bit by directory copy
# This was made from gen-dump.h. attached to issue #103.

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
    trap 'svnwrap' EXIT HUP INT QUIT TERM
    svinit
    dir=$(pwd)
    svnadmin create "$dir/test-repo$$"
    svn co "file://$dir/test-repo$$" test-checkout
    tapcd test-checkout
    mkdir trunk
    svn add trunk
    svn commit -m "Create trunk."
    cd trunk  || (echo "$0: cd failed" >&2; exit 1)
    mkdir dir1
    echo "file" > dir1/file
    svn add dir1
    svn commit -m "Create dir1/file."
    svn propset svn:executable '*' dir1/file
    svn commit -m "Make dir1/file executable."
    svn cp dir1 dir2
    svn commit -m "Copy dir1 to dir2."
    ls -l dir1/file dir2/file
    tapcd ../..
} >/dev/$verbose 2>&1
# shellcheck disable=SC2010
if [ "$dump" = yes ]
then
    svnadmin dump -q test-repo$$
elif ls -l test-checkout/trunk/dir2/file | grep x >/dev/null
then
    echo "ok - $0: executable permission is as expected"
else
    echo "not ok $0: executable permission was expected"
    exit 1
fi
rm -fr test-repo test-checkout
