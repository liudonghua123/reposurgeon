#!/bin/sh
## Test repotool checkout of svn repo

# shellcheck disable=SC1091
. ./common-setup.sh

need svn

trap 'rm -rf /tmp/test-svn-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./svn-to-svn -q -n /tmp/test-svn-repo$$ < vanilla.svn
tapcd /tmp/test-svn-repo$$
${REPOTOOL:-repotool} checkout /tmp/target$$
echo Return code: $? >/tmp/out$$
tapcd -
./dir-md5 /tmp/target$$ >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
#end

