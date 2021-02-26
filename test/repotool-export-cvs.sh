#!/bin/sh
## Test repotool export of CVS repo

command -v cvs >/dev/null 2>&1 || { echo "    Skipped, cvs missing."; exit 0; }
command -v cvs-fast-export >/dev/null 2>&1 || { echo "    Skipped, cvs-fast-export missing."; exit 0; }

trap 'rm -rf /tmp/test-export-repo$$ /tmp/target$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Straight copy of our sample
cp -r hack1.repo/ /tmp/test-export-repo$$

(cd /tmp/test-export-repo$$ >/dev/null || (echo "$0: cd failed" >&2; exit 1); ${REPOTOOL:-repotool} export 2>&1) >/tmp/out$$ 2>&1

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$
	      
#end
