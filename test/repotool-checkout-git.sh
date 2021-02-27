#!/bin/sh
## Test repotool checkout of git repo

# shellcheck disable=SC1091
. ./common-setup.sh

need git

trap 'rm -rf /tmp/test-git-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./fi-to-fi -n /tmp/test-git-repo$$ < simple.fi
tapcd /tmp/test-git-repo$$
${REPOTOOL:-repotool} checkout /tmp/target$$
echo Return code: $? >/tmp/out$$
tapcd -
./dir-md5 /tmp/target$$ >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
#end
