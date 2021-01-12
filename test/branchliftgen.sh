#!/bin/sh
# Generate a Subversion output stream for testing branchlift with mixed commits

set -e

trap 'rm -fr test-repo-$$ test-checkout-$$' EXIT HUP INT QUIT TERM

svnadmin create test-repo-$$
svn checkout --quiet "file://$(pwd)/test-repo-$$" test-checkout-$$

cd test-checkout-$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )

# r1
mkdir trunk branches tags
svn add --quiet trunk branches tags
svn commit --quiet -m 'add trunk branches tags directories'
svn up --quiet

# r2
cd trunk >/dev/null || ( echo "$0: cd failed"; exit 1 )
svn mkdir --quiet nonbranch1
echo foo >nonbranch1/README
svn add --quiet nonbranch1/README
svn commit --quiet -m 'add nonbranch1/README'
svn up --quiet

# r3
svn mkdir --quiet nonbranch2
echo liquid >nonbranch2/DRINKME
svn add --quiet nonbranch2/DRINKME
svn commit --quiet -m 'add nonbranch2/DRINKME'
svn up --quiet

# r4
echo bar >> nonbranch1/README
svn commit --quiet -m 'nonbranch1/README: add bar'
svn up --quiet

# r5 - mixed commit
echo end >> nonbranch1/README
echo sky >> nonbranch2/DRINKME
svn commit --quiet -m 'nonbranch1/README: add end & nonbranch2: add sky'
svn up --quiet

# r6
echo falling >nonbranch2/DRINKME
svn commit --quiet -m 'append to nonbranch2/DRINKME'
svn up --quiet

cd ../.. >/dev/null || ( echo "$0: cd failed"; exit 1 )

# Necessary so we can see repocutter
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }
PATH=$(realpath ..):$(realpath .):${PATH}

# shellcheck disable=1117
svnadmin dump --quiet test-repo-$$ | repocutter -q testify | sed "1a\
\ ## Example of mixed-directory commits on master for testing branchlift
"

# end
