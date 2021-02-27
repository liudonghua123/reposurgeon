#!/bin/sh
## Test repotool mirror of hg repo

# shellcheck disable=SC1091
. ./common-setup.sh

need hg

mode=${1:---regress}

version_gt() { test "$(printf '%s\n' "$@" | sort -V | head -n 1)" != "$1"; }

# shellcheck disable=SC2046
set -- $(git --version)
version="$3"
if version_gt "2.25.1" "$version" && [ "$mode" = "--regress" ]
then
    # 2.20.1 emits terminal resets that 2.25.1 does not.
    echo "not ok - $0: sensitive to Git version skew, seeing unsupported $version # SKIP"
    exit 0
fi

trap 'rm -rf /tmp/test-mirror-repo$$ /tmp/mirror$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Build an example repo
./hg-to-fi -n /tmp/test-mirror-repo$$ <testrepo2.fi
# Then exercise the mirror code to make a copy of it, and dump it.
${REPOTOOL:-repotool} mirror "file://tmp/test-mirror-repo$$" /tmp/mirror$$
(tapcd /tmp/mirror$$; ${REPOTOOL:-repotool} export) >/tmp/out$$ 2>&1

toolmeta "$mode" /tmp/out$$ export
	      
#end
