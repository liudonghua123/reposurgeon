#!/bin/sh
## Test listing tags in CVS repository

# shellcheck disable=SC1091
. ./common-setup.sh

need cvs

trap 'rm -rf /tmp/test-tags-cvs-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Straight copy of our sample
cp -r hack1.repo/ /tmp/test-tags-cvs-repo$$

(tapcd /tmp/test-tags-cvs-repo$$; ${REPOTOOL:-repotool} tags /tmp/target$$) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

toolmeta "$1" /tmp/out$$
	      
# end
