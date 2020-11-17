#!/bin/sh
# Generate a Subversion output stream with a pathological tag pair.
#
# As described in https://gitlab.com/esr/reposurgeon/-/issues/305 With
# reposurgeon 4.20, the Git lift is incorrect. The "replace my-tag tag"
# commit has the wrong parent: it should have the "file: add bar"
# commit as its parent, as in the svn repository.
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

mkdir trunk branches tags
svn add --quiet trunk branches tags
svn commit --quiet -m 'add trunk branches tags directories'

cd trunk >/dev/null || ( echo "$0: cd failed"; exit 1 )
echo foo >file
svn add --quiet file
svn commit --quiet -m 'add file'
svn up --quiet

svn cp --quiet -m 'add my-tag tag' . '^/tags/my-tag'

echo bar >> file
svn commit --quiet -m 'file: add bar'

cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )
svn up --quiet

svn rm tags/my-tag
svn cp trunk tags/my-tag
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
