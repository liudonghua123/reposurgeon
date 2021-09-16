#!/bin/sh
# Generate an SVN stream which may provoke reposurgeon to create a duplicate 'refs/tags/release-1.0-root'
# This used to lift to an invalid input stream, see https://gitlab.com/esr/reposurgeon/-/issues/355

# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

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
svn copy --quiet ^/trunk ^/branches/release-1.0 -m "Create release branch 1.0"
svn up --quiet

# r4
echo bar >>branches/release-1.0/file
svn commit --quiet -m 'Prepare release 1.0'

# r5
svn copy --quiet ^/branches/release-1.0 ^/tags/release-1.0 -m "Tag release 1.0"
svn up --quiet

# r6
svn up --quiet
echo bar >>tags/release-1.0/file
svn commit --quiet -m 'Oops, forgot something! (this turns the "tag" back into a "branch")'

cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )

svndump test-repo-$$ "tag with commit after creation example"

# end
