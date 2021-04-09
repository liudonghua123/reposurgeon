#!/bin/sh
## Test repotool mirror of CVS repo

# This is how we detect we're in Gitlab CI.
if [ -z "${USER}" ]
then
    echo "not ok - $0: ssh is blocked in CI # SKIP"
    exit 0
fi

# shellcheck disable=SC1091
. ./common-setup.sh

need cvs cvs-fast-export

trap 'rm -rf /tmp/mirror$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Then exercise the mirror code to make a copy of it, and dump it.
${REPOTOOL:-repotool} mirror -q "cvs://localhost${PWD}/hack1.repo#module" /tmp/mirror$$
(tapcd /tmp/mirror$$; ${REPOTOOL:-repotool} export 2>&1) >/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$
	      
# end



