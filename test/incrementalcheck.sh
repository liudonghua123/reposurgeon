#! /bin/sh
## Test read of incremental commit sreams.

# This is a simple test to verify that incremenmtal stream import
# works the way we thiunk it does

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

commit refs/heads/master
mark :2
committer Fred J. Muggs <fjm@grobble.com> 0 +0000
data 18
Reverse the data.
from refs/heads/master^0
M 100644 :1 README

EOF
git checkout --quiet
if git log | grep -q "Reverse the data."
then
    echo "ok - $0: incremental import test."
else
    echo "not ok - $0: incremental import test."
    exit 1
fi

# end



