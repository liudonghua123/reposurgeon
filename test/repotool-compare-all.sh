#!/bin/sh
## Test comparing all named locations between svn & git repo

# Should be a multiple-tag, multiple-branch repository
stem=nontipcopy

# No user-serviceable parts below this line

# shellcheck disable=SC1091
. ./common-setup.sh

need svn git

trap 'rm -rf /tmp/test-svn-git-repo$$-svn /tmp/test-svn-git-repo$$-git /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -n /tmp/test-svn-git-repo$$-svn <$stem.svn
reposurgeon "read <${stem}.svn" "prefer git" "rebuild /tmp/test-svn-git-repo$$-git" >/tmp/out$$ 2>&1
${REPOTOOL:-repotool} compare-all -e -root /tmp/test-svn-git-repo$$-svn /tmp/test-svn-git-repo$$-git >/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$
	      
