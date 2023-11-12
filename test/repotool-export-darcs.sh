#!/bin/sh
## Test repotool export of darcs repo

# shellcheck disable=SC1091
. ./common-setup.sh

need darcs

trap 'rm -rf /tmp/test-export-repo$$ /tmp/out$$' EXIT HUP INT QUIT TERM

set -e

# Make a repository from a sample stream and dump it.
darcs convert import --quiet --set-scripts-executable --no-working-dir /tmp/test-export-repo$$ >/dev/null <min.fi || (echo "not ok - darcs import failed"; exit 1)
(
    tapcd /tmp/test-export-repo$$
    ${REPOTOOL:-repotool} export
) >/tmp/out$$

toolmeta "$1" /tmp/out$$

#end

