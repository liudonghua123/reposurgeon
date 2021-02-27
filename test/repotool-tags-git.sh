#!/bin/sh
## Test listing tags in git repository

# shellcheck disable=SC1091
. ./common-setup.sh

need git

trap 'rm -rf /tmp/test-tags-git-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./fi-to-fi -n /tmp/test-tags-git-repo$$ < lighttag.fi
(tapcd /tmp/test-tags-git-repo$$; ${REPOTOOL:-repotool} tags /tmp/target$$) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
# end
