#!/bin/sh
## Test propagation of executable bit by copy to file with rename
# Must be run in reposurgeon/test directory
#
# Perform file copy between directories.
# Expect the resulting dump to have an add with copyfrom at
# the last commit, as opposed to a replace.  Verify that
# the file copy operation leaves the executable bit set.

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
    set -e
    trap 'svnwrap' EXIT HUP INT QUIT TERM
    svninit
    tapcd trunk
    svn mkdir targetdir
    svn mkdir sourcedir
    echo "Source file" >sourcedir/sourcefile.txt
    svn add sourcedir/sourcefile.txt
    svn propset svn:executable on sourcedir/sourcefile.txt
    svn ci -m "Initial commit of example files"
    svn cp sourcedir/sourcefile.txt targetdir
    svn ci -m "Copy of sourcedir/sourcefile.txt to targetdir."
    tapcd ../..
    ls test-checkout$$
} >"/dev/${verbose}" 2>&1

# shellcheck disable=2010
if [ "$dump" = yes ]
then
    svnadmin dump -q test-repo$$
elif ls -l test-checkout$$/trunk/targetdir/sourcefile.txt | grep x >/dev/null
then
    echo "ok - $0: executable permission is as expected"
    exit 0
else
    echo "not ok - $0: executable permission on targetdir/sourcefile was expected"
    exit 1
fi

# end

