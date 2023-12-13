#!/bin/sh
# Generate a Subversion output stream with a pathological tag pair.
#
# The bug report https://gitlab.com/esr/reposurgeon/-/issues/305 With
# reposurgeon 4.20 reported the Git lift is incorrect.
#
# Inspection revealed a content bug.  The "replace my-tag tag" commit
# made from r5 has wrong content in "file": it should have "foo\nbar\n"
# from r4 as content, as in the svn repository But it gets
# the r2 content "foo\n" instead.  The problem is known to go away if
# the tag delete and recreation in r5 are split into separate
# Subversion commits.
#
# Turns out this bug was produced by parallelizing pass 4a.  The tag
# create/delete/rename has to be done in the same order it was
# committed or havoc will ensue - in particular if r5 is processed before
# r2 and r3.  Warning: this is a nondeterministic failure, you may
# falsely appear to dodge it if you reparallelize!
#
# However, this is not the bug the submitter was attempting to
# report. His actual issue are: (1) the Git commit corresponding to r5
# has a wrong parent corresponding to r3 rather than r4, and (2) while
# the file content at each translated Git revision is correct, the
# second file content change corresponding to r4 is missing from the
# translated log.
#
# He further reported that these bugs went away if the operations in
# r5 are split up so the tag removal and replacement happen in
# separate commits.
#
# repocutter see produces this listing:
#
# 1-1   add      branches/
# 1-2   add      tags/
# 1-3   add      trunk/
# 2-1   add      trunk/file
# 3-1   copy     tags/my-tag/ from 2:trunk/
# 4-1   change   trunk/file
# 5-1   delete   tags/my-tag
# 5-2   copy     tags/my-tag/ from 4:trunk/
# 6-1   change   trunk/file
#
# What confuses reposurgeon is revision 5.
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

{
    repository init svn
    repository stdlayout

    # r2
    echo foo >file
    svn add file
    svn commit -m 'add file'
    svn up

    # r3
    svn cp -m 'add my-tag tag' . '^/tags/my-tag'

    # r4
    echo bar >> file
    svn commit -m 'file: add bar'
    tapcd ..
    svn up

    # r5
    svn rm tags/my-tag
    svn cp trunk tags/my-tag
    svn commit -m 'replace my-tag tag'

    tapcd trunk
    echo end >> file
    svn commit -m 'file: add end'
    repository wrap
} >/dev/$verbose 2>&1

if [ "$dump" = yes ]
then
    repository export "tag doublet example"
fi

# end
