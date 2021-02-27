#!/bin/sh
## Test standard CVS to Git workflow

# This is how we detect we're in Gitlab CI.
if [ -z "${USER}" ]
then
    echo "not ok - ssh is blocked in CI, so rsync will fail # SKIP"
    exit 0
fi

# shellcheck disable=SC1091
. ./common-setup.sh

need cvs cvs-fast-export

TMPDIR=${TMPDIR:-/tmp}

trap 'rm -rf ${TMPDIR}/cvs-scratch$$ ${TMPDIR}/ref$$ ${TMPDIR}/out$$' EXIT HUP INT QUIT TERM

# Go to our sandbox
here=$(realpath .)
mkdir "${TMPDIR}/cvs-scratch$$"
tapcd "${TMPDIR}/cvs-scratch$$"

# Make the workflow file.
repotool initialize -q hack1 cvs git

# Convert the repository
make --silent -e REMOTE_URL="cvs://localhost${here}/hack1.repo#module" VERBOSITY="" 2>&1 | sed "/ no commitids before/"d

# Compare the results
repotool compare-all hack1-mirror hack1-git || ( echo "not ok - $0: repositories do not compare equal."; exit 1 )

echo "ok - $0: repositories compare equal"

# No output is good news

#end
