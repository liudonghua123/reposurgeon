#!/bin/sh
## Test repotool export of hg repo

# shellcheck disable=SC1091
. ./common-setup.sh

need hg

mode=${1:---regress}

trap 'rm -rf /tmp/test-export-repo$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Make a repository from the stream
./hg-to-fi -n /tmp/test-export-repo$$ < testrepo2.fi

(tapcd /tmp/test-export-repo$$; ${REPOTOOL:-repotool} export 2>&1) | ./hg-to-fi | sed -e 1d -e '/^#legacy-id/d' | diff --text -u --label testrepo2.fi testrepo2.fi --label repotool-export - >/tmp/out$$ 2>&1

toolmeta "${mode}" /tmp/out$$ export
	      
#end

