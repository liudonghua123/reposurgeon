#! /bin/sh
## Test read of incremental commit sreams.

# This is a simple test to verify that incremenmtal stream import
# works the way we think it does

# This must match fi-to-fi's assumptions
testrepo=${TMPDIR:-/tmp}/test-repo$$

trap 'rm -fr ${testrepo}' EXIT HUP INT QUIT TERM

# Create a simple repository
./fi-to-fi -n <min.fi "${testrepo}"
cd "${testrepo}" >/dev/null || ( echo "$0: base repository creation at ${testrepo} failed"; exit 1 )

# Sanity check
if [ ! -f README ]
then
    echo "$0: test repository does not have expected content"
fi

# Read in an incremental commit
git fast-import --quiet <<EOF
blob
mark :1
data 20
9876543210987654321

reset refs/heads/master
from refs/heads/master^0

commit refs/heads/master
mark :2
committer Fred J. Muggs <fjm@grobble.com> 0 +0000
data 18
Reverse the data.
M 100644 :1 README

EOF
git checkout --quiet

# Check that the last commit is present
git log | grep -q "Reverse the data."
# shellcheck disable=SC2181
if [ "$?" != 0 ]
then
    echo "not ok - $0: incremental import test (content)."
    exit 1
fi

# Check link structure. HEAD^1 is "parent of the head commit"
git log HEAD^1 | grep -q "Second commit"
# shellcheck disable=SC2181
if [ "$?" != 0 ]
then
    echo "not ok - $0: incremental import test (link structure)."
    exit 1
fi

echo "ok - $0: incremental import test."

# end



