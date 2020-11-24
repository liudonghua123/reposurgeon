#!/bin/sh
# Generate a Subversion output stream with a pathological tag pair.
#
# The bug report https://gitlab.com/esr/reposurgeon/-/issues/305 With
# reposurgeon 4.20 reported the Git lift is incorrect.
#
# Inspection revealed a content bug.  The "replace my-tag tag" commit
# made from r5 has wrong content in "file": it should have the "file:
# add bar" from r4 as its parent, as in the svn repository But it gets
# the r2 content "foo" instead.  The problem is known to go away if
# the tag delete and recreation in r5 are split into separate
# Subversion commits.
#
# Turns out this bug was produced by parallelizing pass 4a.  The tag
# create/delete/rename has to be done in the same order it was
# committed or havoc will ensue - in particular if r5 is processed before
# r2 and r3.  Warning: this is a nondetereministic failure, you may
# falsely appear to dodge it if you reparallelize!
#
# However, this is not the bug the submitter was attempting to
# report. His actual issue are: (1) the Git commit corresponfing to r5
# has a wrong parent, and (2) while the file content at each translted
# Git revision is correct, the second file content change
# corresponding to r4 is missing from the translated log.
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

set -e

trap 'rm -fr test-repo-$$ test-checkout-$$' EXIT HUP INT QUIT TERM

svnadmin create test-repo-$$
svn checkout --quiet "file://$(pwd)/test-repo-$$" test-checkout-$$

cd test-checkout-$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )

# r1
mkdir trunk branches tags
svn add --quiet trunk branches tags
svn commit --quiet -m 'add trunk branches tags directories'

# r2
cd trunk >/dev/null || ( echo "$0: cd failed"; exit 1 )
echo foo >file
svn add --quiet file
svn commit --quiet -m 'add file'
svn up --quiet

# r3
svn cp --quiet -m 'add my-tag tag' . '^/tags/my-tag'

# r4
echo bar >> file
svn commit --quiet -m 'file: add bar'
cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )
svn up --quiet

# r5
svn rm --quiet tags/my-tag
svn cp --quiet trunk tags/my-tag
svn commit --quiet -m 'replace my-tag tag'

cd trunk >/dev/null || ( echo "$0: cd failed"; exit 1 )
echo end >> file
svn commit --quiet -m 'file: add end'

cd ../.. >/dev/null || ( echo "$0: cd failed"; exit 1 )

# Necessary so we can see repocutter
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }
PATH=$(realpath ..):$(realpath .):${PATH}

# shellcheck disable=1117
svnadmin dump --quiet test-repo-$$ | repocutter -q testify | sed "1a\
\ ## tag doublet example
"

# end
