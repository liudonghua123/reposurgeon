#!/bin/sh
# Generate a Subversion output stream with a "clean" tag (1.0) and one that was committed to after tagging (2.0).
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

{
    repository init svn
    repository stdlayout
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

    repository wrap
} >/dev/$verbose 2>&1


if [ "$dump" = yes ]
then
    repository export "tag with commit after creation example"
fi
  
# end
