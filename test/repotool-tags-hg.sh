#!/bin/sh
## Test listing tags in hg repository

# shellcheck disable=SC1091
. ./common-setup.sh

need hg git

trap 'rm -rf /tmp/test-tags-hg-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./hg-to-fi -n /tmp/test-tags-hg-repo$$ <lighttag.fi
(tapcd /tmp/test-tags-hg-repo$$; ${REPOTOOL:-repotool} tags /tmp/target$$) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
# end
