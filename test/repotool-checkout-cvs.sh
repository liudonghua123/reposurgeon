#!/bin/sh
## Test repotool checkout of CVS repo

command -v cvs >/dev/null 2>&1 || { echo "    Skipped, cvs missing."; exit 0; }

trap 'rm -rf /tmp/test-cvs-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

set -e	# So we'll crap out if hack-repo does not exist
cp -r hack1.repo/ /tmp/test-cvs-repo$$
(cd /tmp/test-cvs-repo$$ >/dev/null; ${REPOTOOL:-repotool} checkout /tmp/target$$; echo Return code: $? >/tmp/out$$ 2>&1)
rm -rf /tmp/target$$/CVS/	# CVS internal use, and contents are different every time
./dir-md5 /tmp/target$$  >>/tmp/out$$

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$

#end
