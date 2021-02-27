#!/bin/sh
## Test repotool export of git repo

# shellcheck disable=SC1091
. ./common-setup.sh

need git

mode=${1:---regress}

version_gt() { test "$(printf '%s\n' "$@" | sort -V | head -n 1)" != "$1"; }

# shellcheck disable=SC2046
set -- $(git --version)
version="$3"
if version_gt "2.25.1" "$version" && [ "$mode" = "--regress" ]
then
    # 2.20.1 emits terminal resets that 2.25.1 does not.
    echo "not ok - sensitive to Git version skew, seeing unsupported $version # SKIP"
    exit 0
fi

trap 'rm -rf /tmp/test-export-repo$$ /tmp/out$$' EXIT HUP INT QUIT TERM

./fi-to-fi -n /tmp/test-export-repo$$ <simple.fi
(tapcd /tmp/test-export-repo$$; ${REPOTOOL:-repotool} export 2>&1) >/tmp/out$$ 2>&1

toolmeta "$mode" /tmp/out$$ export

#end

