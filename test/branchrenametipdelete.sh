#!/bin/sh
# Generate a Subversion output stream with a deleted branch (that also contains spaces)

set -e

trap 'rm -fr test-repo-$$ test-checkout-$$' EXIT HUP INT QUIT TERM

svnadmin create test-repo-$$
svn --quiet checkout "file://$(pwd)/test-repo-$$" test-checkout-$$

cd test-checkout-$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )

# r1
mkdir -p trunk branches tags
svn --quiet add trunk branches tags
svn --quiet commit -m "create initial folder structure"

# r2
echo "initial content" >trunk/file
svn --quiet add trunk/file
svn --quiet commit -m "add initial content"

# r3
echo "more content" >>trunk/file
svn --quiet commit -m "continue development"

# r4
svn --quiet copy "trunk" "branches/first-branch"
svn --quiet commit -m "copy trunk to first new branch"
svn --quiet up

# r5
echo "even more branch content" >>"branches/first-branch/file"
svn --quiet commit -m "continue development on first branch"
svn --quiet up

# r6
svn --quiet rm "branches/first-branch"
svn --quiet commit -m "delete first branch"
svn --quiet up

# r7
svn --quiet copy "trunk" "branches/second-branch"
svn --quiet commit -m "copy trunk to new branch"
svn --quiet up

# r8
echo "even more branch content" >>"branches/second-branch/file"
svn --quiet commit -m "continue development on branch"

# r9
echo "even more trunk content" >>trunk/file
svn --quiet commit -m "continue trunk development"


# ===========================

cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )

# Necessary so we can see repocutter
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }
PATH=$(realpath ..):$(realpath .):${PATH}

# shellcheck disable=1117,1004
svnadmin dump --quiet  test-repo-$$ | repocutter -q testify | sed '1a\
 ## branch with spaces deletion example
 # Generated - do not hand-hack!
'

# end
