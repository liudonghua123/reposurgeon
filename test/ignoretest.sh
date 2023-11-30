#!/bin/sh
## Test ignore features

set -e

# shellcheck disable=SC1091
. ./common-setup.sh

fail() {
    echo "not ok - $*"
}

for vcs in git hg bzr src;
do
    if command -v "$vcs" >/dev/null
    then
	ignore () {
	    repository ignore
	    # shellcheck disable=SC2154
	    repository ignore "${ignorefile}"
	    repository ignore "$1"
	}
	require_empty () {
	    if [ -n "$($1)" ]; then fail "$2"; fi
	}
	
	repository init $vcs /tmp/ignoretest$$ 
	case $vcs in
	    git|hg|bzr|brz|src)	
		touch ignorable
		(repository status | grep '?[ 	]*ignorable' >/dev/null) || fail "${vcs} status didn't flag junk file"
		ignore ignorable
		require_empty "repository status" "${vcs} basic ignore failed"
		ignore ignor*
		require_empty "repository status" "${vcs} check for * wildcard failed"
		ignore ignora?le
		require_empty "repository status" "${vcs} check for ? wildcard failed"
		ignore ignorab[klm]e
		require_empty "repository status" "${vcs} check for range syntax failed"
		ignore ignorab[k-m]e
		require_empty "repository status" "${vcs} check for dash in ranges failed"
		ignore ignorab[!x-z]e
		require_empty "repository status" "${vcs} check for !-negated ranges failed"
		# Temporarily appeasing shellcheck.
		#ignore ignorab[^x-z]e
		#require_empty "repository status" "${vcs} check for ^-negated ranges failed"
		echo "ok - ignore-pattern tests for ${vcs} wrapup." 
		;;
	    *)
		echo "not ok -- no handler for $vcs"
	esac
    else
        printf 'not ok: %s missing # SKIP\n' "$vcs"
    fi
done
#
