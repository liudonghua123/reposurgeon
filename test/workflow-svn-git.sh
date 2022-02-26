#!/bin/sh
## Test standard SVN to Git workflow

# shellcheck disable=SC1091
. ./common-setup.sh

need svn svnadmin

TMPDIR=${TMPDIR:-/tmp}

trap 'rm -rf ${TMPDIR}/scratch$$ ${TMPDIR}/ref$$ ${TMPDIR}/out$$ ${TMPDIR}/diff$$' EXIT HUP INT QUIT TERM

# Go to our sandbox
testdir=$(realpath .)
mkdir "${TMPDIR}/scratch$$"
tapcd "${TMPDIR}/scratch$$"

# Make a repository from a sample stream.
"${testdir}/svn-to-svn" -q -n vanilla-prime <"${testdir}/vanilla.svn"

# Make the workflow file.
repotool initialize -q vanilla-secundus svn git || ( echo "not ok - $0: initialization failed"; exit 1)

# Mirror vanilla-prime into vanilla-secundus and invoke standard workflow
make --silent -e REMOTE_URL="file://${TMPDIR}/scratch$$/vanilla-prime" VERBOSITY="" >/dev/null 2>&1  || ( echo "not ok - $0: mirror and conversion failed"; exit 0)

# Compare the results
repotool compare-all vanilla-secundus-mirror vanilla-secundus-git >"${TMPDIR}/diff$$" 
if [ -s "${TMPDIR}/diff$$" ]
then
    echo "not ok - $0: repositories do not compare equal."
    echo "  --- |"
    sed <"/tmp/warnings$$" -e 's/^/  /'
    sed <"/tmp/diff$$" -e 's/^/  /'
    echo "  ..."
    exit 1
else
    echo "ok - $0: repositories compared equal";
    exit 0
fi

#end
