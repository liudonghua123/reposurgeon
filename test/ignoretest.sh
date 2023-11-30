#!/bin/sh
## Test ignore features

set -e

# shellcheck disable=SC1091
. ./common-setup.sh

fail() {
    echo "not ok - $*"
}

for vcs in git hg bzr brz src;
do
    if command -v "$vcs" >/dev/null
    then
	ignore () {
	    repository ignore
	    # shellcheck disable=SC2154
	    repository ignore "${ignorefile}"
	    repository ignore "$1"
	}
	no_status_output () {
	    legend="$1"
	    exceptions="$2"
	    if [ -n "${exceptions}" ] && expr "$vcs}" : "${exceptions}" >/dev/null
	    then
		if [ -z "$(repository status)" ]; then success=no; fail "${vcs} ${legend} unexpectedly succeeded"; fi
	    else
		if [ -n "$(repository status)" ]; then success=no; fail "${vcs} ${legend} unexpectedly failed"; fi
	    fi
	}
	
	repository init $vcs /tmp/ignoretest$$
	case $vcs in
	    git|hg|bzr|brz|src)
		success=yes
		touch 'ignorable'
		(repository status | grep '?[ 	]*ignorable' >/dev/null) || fail "${vcs} status didn't flag junk file"
		ignore 'ignorable'
		no_status_output "basic ignore"
		ignore 'ignor*'
		no_status_output "check for * wildcard"
		ignore 'ignora?le'
		no_status_output "check for ? wildcard" "hg"
		ignore 'ignorab[klm]e'
		no_status_output "check for range syntax"
		ignore 'ignorab[k-m]e'
		no_status_output "check for dash in ranges"
		ignore 'ignorab[!x-z]e'
		no_status_output "check for !-negated ranges" "hg"
		ignore 'ignorab[^x-z]e'
		no_status_output "check for ^-negated ranges" "src"
		if [ "${success}" = "yes" ]
		then
		    echo "ok - ignore-pattern tests for ${vcs} went as expected."
		fi
		;;
	    *)
		echo "not ok -- no handler for $vcs"
	esac
    else
        printf 'not ok: %s missing # SKIP\n' "$vcs"
    fi
done
#
