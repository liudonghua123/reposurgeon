#!/bin/sh
## Test repotool mirror of svn repo via rsync

command -v svn >/dev/null 2>&1 || { echo "    Skipped, svn missing."; exit 0; }
command -v rsync >/dev/null 2>&1 || { echo "    Skipped, rsync missing."; exit 0; }

case $1 in
    --regress)
        if ! ssh -o PasswordAuthentication=no localhost 2>/dev/null; then
            echo "SKIPPED - this test needs to be able to ssh to localhost, but that doesn't appear to be possible"
            exit 0;
        fi;;
esac

trap 'rm -rf /tmp/test-repo-$$ /tmp/out$$ /tmp/mirror$$' EXIT HUP INT QUIT TERM

# This is how we detect we're in Gitlab CI.
if [ -z "${USER}" ]
then
    echo "SKIPPED - ssh is blocked in CI, so rsync will fail"
    exit 0
fi

# Make a repository from a sample stream.
./svn-to-svn -q -n /tmp/test-repo-$$ <vanilla.svn
# Then exercise the mirror code to make a copy of it.
${REPOTOOL:-repotool} mirror rsync://localhost/tmp/test-repo-$$ /tmp/mirror$$

# This test can fail spuriously due to format skew.  Kevin Caswick
# explains:
# > Note: Test repotool export of svn repo fails on svnadmin, version
# > 1.6.11 as the dump is sorted differently, moving svn:log before
# > svn:author instead of after svn:date. It works fine on svnadmin,
# > version 1.8.10.
(cd /tmp/mirror$$ >/dev/null || ( echo "$0: cd failed" >&2; exit 1 ); ${REPOTOOL:-repotool} export) >/tmp/out$$

# This test generates randomly time-varying UUIDs.
case $1 in
    --regress)
        sed </tmp/out$$ -e '/UUID:/d' | diff --text -u repotool-mirror-rsync.chk - || ( echo "$0: FAILED"; exit 1 ); ;;
    --rebuild)
	sed </tmp/out$$ -e '/UUID:/d' >repotool-mirror-rsync.chk;;
    --view)
	cat /tmp/out$$;;
esac

#end

