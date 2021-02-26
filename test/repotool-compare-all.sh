#!/bin/sh
## Test comparing all named locations between svn & git repo

# Should be a multiple-tag, multiple-branch repository
stem=nontipcopy

# No user-serviceable parts below this line

command -v svn >/dev/null 2>&1 || { echo "    Skipped, svn missing."; exit 0; }
command -v git >/dev/null 2>&1 || { echo "    Skipped, git missing."; exit 0; }

trap 'rm -rf /tmp/test-svn-git-repo$$-svn /tmp/test-svn-git-repo$$-git /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -n /tmp/test-svn-git-repo$$-svn <$stem.svn
reposurgeon "read <${stem}.svn" "prefer git" "rebuild /tmp/test-svn-git-repo$$-git" >/tmp/out$$ 2>&1
${REPOTOOL:-repotool} compare-all -e -root /tmp/test-svn-git-repo$$-svn /tmp/test-svn-git-repo$$-git >/tmp/out$$ 2>&1

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$
	      
