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
	ignorecheck () {
	    Z=-z
	    N=-n
	    if [ "$1" = '--nomatch' ]
	    then
		Z=-n
		N=-z
		shift
	    fi
	    pattern="$1"
	    match="$2"
	    legend="$3"
	    exceptions="$4"
	    count=$((count+1))
	    repository ignore
	    # shellcheck disable=SC2154
	    repository ignore "${ignorefile}"
	    repository ignore "${pattern}"
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
		ignorecheck 'ignorable' 'ignorable' "basic ignore"
		ignorecheck 'ignor*' 'ignorable' "check for * wildcard"
		ignorecheck 'ignora?le' 'ignorable' "check for ? wildcard" "hg"	# ignQUESTION
		ignorecheck 'ignorab[klm]e' 'ignorable' "check for range syntax"
		ignorecheck 'ignorab[k-m]e' 'ignorable' "check for dash in ranges"
		ignorecheck 'ignorab[!x-z]e' 'ignorable' "check for !-negated ranges" "hg"	# ignBANGDASH
		ignorecheck 'ignorab[^x-z]e' 'ignorable' "check for ^-negated ranges" "src"	# ignCARETDASH
		ignorecheck --nomatch '\*' 'ignorable' "check for backslash escaping" "b[rz][rz]"	# ignBACKSLASH
		rm ignorable
		mkdir foo
		touch foo/bar
		# These tests fail because the git and hg status commands
		# do things that don't fit the rwa
		if [ "${vcs}" != "hg" ] && [ "${vcs}" != "git" ]
		then
		    ignorecheck --nomatch 'foo?bar' 'bar' "check for ? not matching /"
		    ignorecheck --nomatch '*bar' 'bar' "check for * not matching /"
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
