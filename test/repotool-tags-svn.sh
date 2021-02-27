#!/bin/sh
## Test listing tags in svn repository

# shellcheck disable=SC1091
. ./common-setup.sh

need svn

trap 'rm -rf /tmp/test-tags-svn-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -n /tmp/test-tags-svn-repo$$ <simpletag.svn
(tapcd /tmp/test-tags-svn-repo$$; ${REPOTOOL:-repotool} tags /tmp/target$$) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
# end
