#!/bin/sh
## Test repotool mirror of svn repo via svnsync

# /tmp/test-repo-fubar has a fixed name because it gets generated
# into the checkfile as the value of the svn:sync-from-url
# property.  If that changes on each run it's going to cause
# spurious test failures

# shellcheck disable=SC1091
. ./common-setup.sh

need svn

trap 'rm -rf /tmp/test-repo-fubar /tmp/out$$ /tmp/mirror$$' EXIT HUP INT QUIT TERM

# Make a repository from a sample stream.
./svn-to-svn -q -n /tmp/test-repo-fubar <vanilla.svn
# Then exercise the mirror code to make a copy of it.
${REPOTOOL:-repotool} mirror -q file:///tmp/test-repo-fubar /tmp/mirror$$

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

# end

