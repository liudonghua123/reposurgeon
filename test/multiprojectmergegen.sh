#!/bin/sh
# Generate an SVN dump of multiple projects that are later merged into a single trunk

set -e

trap 'rm -fr test-repo-$$ test-checkout-$$' EXIT HUP INT QUIT TERM

svnadmin create test-repo-$$
svn checkout --quiet "file://$(pwd)/test-repo-$$" test-checkout-$$

cd test-checkout-$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )

projects="software firmware docs"

# r1
for project in $projects; do
    for dir in trunk branches tags
    do
        mkdir -p $project/$dir
    done
    svn add --quiet $project
done
svn commit --quiet -m 'init multi-project repo'

# r2, r3, r4
for project in $projects; do
    echo "initial $project content" >$project/trunk/$project.txt
    svn add --quiet $project/trunk/$project.txt
    svn commit --quiet -m "initial $project commit"
done



# # FIXME: if you add intermediate commits here (and adjust `merge` commands accordingly), no invalid import stream is generated.
# # So the bug seems to only affect merges from root commits.
# for project in $projects; do
#     echo "some early changes on $project" >>$project/trunk/$project.txt
#     svn commit --quiet -m "second $project commit"
# done



# r5
mkdir trunk branches tags
svn add --quiet trunk branches tags
svn commit --quiet -m "We don't want to develop separate projects anymore! Prepare for one single trunk."

# r6, r7, r8
for project in $projects; do
    svn copy --quiet $project/trunk trunk/$project
    svn commit --quiet -m "copy $project to new trunk"
done
svn --quiet up

#r9
for project in $projects; do
    echo "continue $project development" >>trunk/$project/$project.txt
done
svn commit --quiet -m "continue development on new trunk"


cd .. >/dev/null || ( echo "$0: cd failed"; exit 1 )

# Necessary so we can see repocutter
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }
PATH=$(realpath ..):$(realpath .):${PATH}

# shellcheck disable=1117
svnadmin dump --quiet test-repo-$$ | repocutter -q testify | sed "1a\
\ ## multiple projects merged into common trunk example
"

# end
