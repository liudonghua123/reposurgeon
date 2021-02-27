#!/bin/sh
## Test comparing tag between svn and git repo

# Results should be independent of what file stem this is, as
# long as it's an svn dump and has the right festure to be named by cmploc.
stem=simpletag
cmploc=tag1
cmpmode=-t

# No user-serviceable parts below this line

# shellcheck disable=SC1091
. ./common-setup.sh

need svn git

trap 'rm -rf /tmp/test-repo$$-svn /tmp/test-repo$$-svn-checkout /tmp/test-repo$$-git /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -c /tmp/test-repo$$-svn <${stem}.svn
reposurgeon "read <${stem}.svn" "prefer git" "rebuild /tmp/test-repo$$-git" >/tmp/out$$ 2>&1
${REPOTOOL:-repotool} compare ${cmpmode} ${cmploc} /tmp/test-repo$$-svn-checkout /tmp/test-repo$$-git | sed -e "s/$$/\$\$/"g >>/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$
	      
# end
