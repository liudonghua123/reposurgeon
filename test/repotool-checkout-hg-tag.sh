#!/bin/sh
## Test repotool checkout of Mercurial repo at tag

# shellcheck disable=SC1091
. ./common-setup.sh

need hg git

trap 'rm -rf /tmp/test-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./hg-to-fi -n /tmp/test-repo$$ < simple.fi
tapcd /tmp/test-repo$$
${REPOTOOL:-repotool} checkout -t lightweight-sample /tmp/target$$
echo Return code: $? >/tmp/out$$
tapcd -
./dir-md5 /tmp/target$$ >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
#end
