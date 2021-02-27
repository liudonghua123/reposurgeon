#!/bin/sh
## Test repotool export of svn repo

# shellcheck disable=SC1091
. ./common-setup.sh

need svn

trap 'rm -rf /tmp/test-export-repo$$ /tmp/out$$' EXIT HUP INT QUIT TERM

# Make a repository from a sample stream.
./svn-to-svn -q -n /tmp/test-export-repo$$ <vanilla.svn

# This test can fail spuriously due to format skew.  Kevin Caswick
# explains:
# > Note: Test repotool export of svn repo fails on svnadmin, version
# > 1.6.11 as the dump is sorted differently, moving svn:log before
# > svn:author instead of after svn:date. It works fine on svnadmin,
# > version 1.8.10.
(tapcd /tmp/test-export-repo$$; ${REPOTOOL:-repotool} export) >/tmp/out$$

toolmeta "$1" /tmp/out$$

#end

