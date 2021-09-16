#!/bin/sh
# This demonstrates the behavior descrebed in  
# https://gitlab.com/esr/reposurgeon/-/issues/357
#
# The sequences of operations is this:
#
# 1-1   add      branches/
# 1-2   add      tags/
# 1-3   add      trunk/
# 2-1   add      trunk/file
# 3-1   change   trunk/file
# 4-1   add      branches/release-1.0/
# 5-1   copy     branches/release-1.0/file from 4:trunk/file
# 6-1   change   branches/release-1.0/file
# 7-1   change   trunk/file
#
# This ought to be turned into a branch copy, but every
# attempt to do so has created more problems than it solved.
#
# If you fix this, don't forget to modify or delete the note
# in README.adoc.

# This is a GENERATOR

set -e

# shellcheck disable=SC1091
. ./common-setup.sh

trap 'rm -fr test-repo-$$ test-checkout-$$' EXIT HUP INT QUIT TERM

svnadmin create test-repo-$$
svn checkout --quiet "file://$(pwd)/test-repo-$$" test-checkout-$$

cd test-checkout-$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )

# r1
mkdir -p trunk branches tags
svn add --quiet trunk branches tags
svn commit --quiet -m "create initial folder structure"

# r2
echo "initial content" >trunk/file
svn add --quiet trunk/file
svn commit --quiet -m "add initial content"

# r3
echo "more content" >>trunk/file
svn commit --quiet -m "continue development"

# r4
mkdir -p branches/release-1.0
svn add --quiet branches/release-1.0
svn commit --quiet -m "prepare empty release branch"
svn --quiet up

# r5
svn copy --quiet trunk/* branches/release-1.0
svn commit --quiet -m "copy everything from trunk to release branch"
svn --quiet up

# r6
echo "even more branch content" >>branches/release-1.0/file
svn commit --quiet -m "continue development on branch"

# r7
echo "even more trunk content" >>trunk/file
svn commit --quiet -m "continue trunk development"

cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )

svndump test-repo-$$ "branch creation via copy-to-empty-dir example"

# end
