#!/bin/sh
## Test detecting svn-to-git conversion failure

# We need to intoducd detectible content that was not in the correctly
# converted version.  Whatxvi we're testing here is the ability of
# repotool compare to notice this,
stem=vanilla
cat >/tmp/altered$$ <<EOF
Event-Number: 7
Event-Mark: :6

This is deliberately corrupted data for a blob.
EOF

# No user-serviceable parts below this line

# shellcheck disable=SC1091
. ./common-setup.sh

need svn git

trap 'rm -rf /tmp/test-repo$$-svn /tmp/test-repo$$-git /tmp/test-repo$$-svn-checkout /tmp/out$$ /tmp/altered$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -c /tmp/test-repo$$-svn /tmp/test-repo$$-svn-checkout <${stem}.svn
reposurgeon "read <${stem}.svn" "msgin --blobs </tmp/altered$$" "prefer git" "rebuild /tmp/test-repo$$-git" >/tmp/out$$ 2>&1
# shellcheck disable=SC2086
${REPOTOOL:-repotool} compare ${TESTOPT} /tmp/test-repo$$-svn-checkout /tmp/test-repo$$-git 2>&1 | sed -e "s/$$/\$\$/"g >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
# end
