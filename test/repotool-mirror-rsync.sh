#!/bin/sh
## Test repotool mirror of svn repo via rsync

# shellcheck disable=SC1091
. ./common-setup.sh

need svn rsync

case $1 in
    --regress)
        if ! ssh -o PasswordAuthentication=no -n localhost true 2>/dev/null 1>&2; then
            echo "not ok - $0: this test needs to be able to ssh to localhost, but that doesn't appear to be possible # SKIP"
            exit 0;
        fi;;
esac

trap 'rm -rf /tmp/test-repo-$$ /tmp/out$$ /tmp/mirror$$' EXIT HUP INT QUIT TERM

# This is how we detect we're in Gitlab CI.
if [ -z "${USER}" ]
then
    echo "not ok - $0: ssh is blocked in CI, so rsync will fail # SKIP"
    exit 0
fi

# Make a repository from a sample stream.
./svn-to-svn -q -n /tmp/test-repo-$$ <vanilla.svn
# Then exercise the mirror code to make a copy of it.
${REPOTOOL:-repotool} mirror -q rsync://localhost/tmp/test-repo-$$ /tmp/mirror$$

# This test can fail spuriously due to format skew.  Kevin Caswick
# explains:
# > Note: Test repotool export of svn repo fails on svnadmin, version
# > 1.6.11 as the dump is sorted differently, moving svn:log before
# > svn:author instead of after svn:date. It works fine on svnadmin,
# > version 1.8.10.
(tapcd /tmp/mirror$$; ${REPOTOOL:-repotool} export) >/tmp/out$$

# This test generates randomly time-varying UUIDs.
stem=$(echo "$0" | sed -e 's/.sh//')
case $1 in
    --regress)
	legend=$(sed -n '/^## /s///p' <"$0" 2>/dev/null);
        sed </tmp/out$$ -e '/UUID:/d' | QUIET=${QUIET} ./tapdiffer "${legend}" "${stem}.chk"; ;;
    --rebuild)
	sed </tmp/out$$ -e '/UUID:/d' >"${stem}.chk";;
    --view)
	cat /tmp/out$$;;
    *)
        echo "not ok - $0: unknown mode $1 # SKIP";; 
esac

#end

