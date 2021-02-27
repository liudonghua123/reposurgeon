#!/bin/sh
## Test repotool export of CVS repo

# shellcheck disable=SC1091
. ./common-setup.sh

need cvs cvs-fast-export

trap 'rm -rf /tmp/test-export-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Straight copy of our sample
cp -r hack1.repo/ /tmp/test-export-repo$$

(tapcd /tmp/test-export-repo$$; ${REPOTOOL:-repotool} export 2>&1) >/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$ export

#end
