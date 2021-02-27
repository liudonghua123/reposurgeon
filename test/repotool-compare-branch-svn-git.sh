#!/bin/sh
## Test comparing branch between svn and git repo

# Results should be independent of what file stem this is, as
# long as it's an svn dump and has the right feature to be named by cmploc.
stem=debranch
cmploc=resources
cmpmode=-b

# No user-serviceable parts below this line

# shellcheck disable=SC1091
. ./common-setup.sh

need svn git

trap 'rm -rf /tmp/test-svn-git-repo$$-svn /tmp/test-svn-git-repo$$-svn-checkout /tmp/test-svn-git-repo$$-git /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -c /tmp/test-svn-git-repo$$-svn <${stem}.svn
reposurgeon "read <${stem}.svn" "prefer git" "rebuild /tmp/test-svn-git-repo$$-git" >/tmp/out$$ 2>&1
${REPOTOOL:-repotool} compare ${cmpmode} ${cmploc} /tmp/test-svn-git-repo$$-svn-checkout /tmp/test-svn-git-repo$$-git | sed -e "s/$$/\$\$/"g >>/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$
	      
# end
