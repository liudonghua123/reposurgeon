#!/bin/sh
## Test ignore features

set -e

# shellcheck disable=SC1091
. ./common-setup.sh

fail() {
    echo "not ok - $*"
}

success=yes
count=0
failures=0
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
	    Z=-z
	    N=-n
	    if [ "$1" = '--nomatch' ]
	    then
		Z=-n
		N=-z
		shift
	    fi
	    legend="$1"
	    exceptions="$2"
	    count=$((count+1))
	    if [ -n "${exceptions}" ] && expr "$vcs}" : "${exceptions}" >/dev/null
	    then
		# shellcheck disable=1072,1073,1009
		if [ $Z "$(repository status)" ]; then failures=$((failures+1)); fail "${vcs} ${legend} unexpectedly succeeded"; fi
	    else
		# shellcheck disable=1072,1073,1009
		if [ $N "$(repository status)" ]; then failures=$((failures+1)); fail "${vcs} ${legend} unexpectedly failed"; fi
	    fi
	}
	
	repository init $vcs /tmp/ignoretest$$
	case $vcs in
	    git|hg|bzr|brz|src)
		touch 'ignorable'
		(repository status | grep '?[ 	]*ignorable' >/dev/null) || fail "${vcs} status didn't flag junk file"
		ignore 'ignorable'
		no_status_output "basic ignore"
		ignore 'ignor*'
		no_status_output "check for * wildcard"
		ignore 'ignora?le'
		no_status_output "check for ? wildcard" "hg"	# ignQUESTION
		ignore 'ignorab[klm]e'
		no_status_output "check for range syntax"
		ignore 'ignorab[k-m]e'
		no_status_output "check for dash in ranges"
		ignore 'ignorab[!x-z]e'
		no_status_output "check for !-negated ranges" "hg"	# ignBANGDASH
		ignore 'ignorab[^x-z]e'
		no_status_output "check for ^-negated ranges" "src"	# ignCARETDASH
		ignore '\*'
		no_status_output --nomatch "check for backslash escaping" "b[rz][rz]"	# ignBACKSLASH
		rm ignorable
		mkdir a
		touch a/c
		# These tests fail because the git and hg status commands
		# do things that don't fit the rwa
		if [ "${vcs}" != "hg" ] && [ "${vcs}" != "git" ]
		then
		    ignore 'a?c'
		    no_status_output --nomatch "check for ? not matching /"
		    ignore '*c'
		    no_status_output --nomatch "check for * not matching /"
		fi
		;;
	    *)
		echo "not ok -- no handler for $vcs"
		failures=$((failures+1))
	esac
    else
        printf 'not ok: %s missing # SKIP\n' "$vcs"
	failures=$((failures+1))
    fi
done

echo "ok - ${failures} of ${count} ignore-pattern tests failed."

#end
