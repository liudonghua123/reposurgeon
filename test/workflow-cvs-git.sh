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

trap 'rm -rf ${TMPDIR}/cvs-scratch$$ ${TMPDIR}/ref$$ ${TMPDIR}/out$$ ${TMPDIR}/diff$$' EXIT HUP INT QUIT TERM

# Go to our sandbox
here=$(realpath .)
mkdir "${TMPDIR}/cvs-scratch$$"
tapcd "${TMPDIR}/cvs-scratch$$"

# Make the workflow file.
repotool initmake -q hack1 cvs git

# Convert the repository
# These variables are unset so the following make invocation won't try to
# use the make server instance set up by the main make invocation.  When the
# variables remain set, you can get obscure error messages and hangs.
unset MAKEFLAGS MFLAGS MAKELEVEL MAKE_TERMERR MAKE_TERMOUT
make --silent -e REMOTE_URL="cvs://localhost${here}/hack1.repo#module" VERBOSITY="" >/dev/null 2>&1 | sed "/ no commitids before/"d >"${TMPDIR}/diff$$" ||  ( echo "not ok - $0: mirror and conversion failed"; exit 0)

# Compare the results
repotool compare-all hack1-mirror hack1-git >"${TMPDIR}/diff$$"
if [ -s "${TMPDIR}/diff$$" ]
then
    echo "not ok - $0: repositories do not compare equal."
    tapdump "/tmp/diff$$"
    exit 1
else
    echo "ok - $0: repositories compare equal"
    exit 0
fi

#end
