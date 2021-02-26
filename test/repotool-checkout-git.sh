#!/bin/sh
## Test repotool checkout of git repo

command -v git >/dev/null 2>&1 || { echo "    Skipped, git missing."; exit 0; }

trap 'rm -rf /tmp/test-git-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./fi-to-fi -n /tmp/test-git-repo$$ < simple.fi
cd /tmp/test-git-repo$$ >/dev/null || ( echo "$0: cd failed"; exit 1 )
${REPOTOOL:-repotool} checkout /tmp/target$$
echo Return code: $? >/tmp/out$$
cd - >/dev/null || ( echo "$0: cd failed"; exit 1 )
./dir-md5 /tmp/target$$ >>/tmp/out$$

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$
	      
#end
