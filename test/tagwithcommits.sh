#!/bin/sh
# Generate a Subversion output stream with a "clean" tag (1.0) and one that was commited to after tagging (2.0).

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
echo foo >trunk/file
svn add --quiet trunk/file
svn commit --quiet -m 'add file'

# r3
svn copy --quiet ^/trunk ^/tags/1.0 -m "Tag Release 1.0"

# r4
svn copy --quiet ^/trunk ^/tags/2.0 -m "Tag Release 2.0"

# r5
svn up --quiet
echo bar >>tags/2.0/file
svn commit --quiet -m 'Commit to Release 2.0 after tagging'

cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )

# Necessary so we can see repocutter
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }
PATH=$(realpath ..):$(realpath .):${PATH}

# shellcheck disable=1117,1004
svnadmin dump --quiet test-repo-$$ | repocutter -q testify | sed '1a\
 ## tag with commit after creation example
 # Generated - do not hand-hack!
'

# end
