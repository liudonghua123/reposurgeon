#!/bin/sh
## Test ignore features

set -e

# shellcheck disable=SC1091
. ./common-setup.sh

fail() {
    echo "not ok - $*"
}

# Temporary warning disable until we check more systems
# shellcheck disable=SC2043
for vcs in hg;
do
    if command -v "$vcs" >/dev/null
    then
	clear () {
	    rm -f ".${vcs}ignore"
	    echo ".${vcs}ignore" >>".${vcs}ignore"
	}
	ignore () {
	    clear
	    echo "$1" >>".${vcs}ignore"
	}
	require_empty () {
	    if [ -n "$($1)" ]; then fail "$2"; fi
	}
	
	repository init $vcs /tmp/ignoretest$$ 
	case $vcs in
	    hg)	
		touch ignorable
		(${vcs} status | grep '^? ignorable' >/dev/null) || fail "${vcs} status didn't flag junk file"
		ignore .hgignore
		ignore ignorable
		require_empty "${vcs} status" "${vcs} basic ignore failed"
		ignore ignora?le
		require_empty "${vcs} status" "${vcs} check for ? wildcard failed"
		ignore ignorab[klm]e
		require_empty "${vcs} status" "${vcs} check for range syntax failed"
		ignore ignorab[k-m]e
		require_empty "${vcs} status" "${vcs} check for dash in ranges failed"
		echo "ok - all ignore-pattern tests for ${vcs} succeeded." 
		;;
	    *)
		echo "not ok -- no handler for $vcs"
	esac
    else
        printf 'not ok: %s missing # SKIP\n' "$vcs"
    fi
done
#
