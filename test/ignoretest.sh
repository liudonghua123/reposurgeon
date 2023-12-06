#!/bin/sh
## Test ignore features
#
# Outputs one line of TAP on success.  On failures, muktiple TAP lines
# each with the offending status-command dump following as a YAML
# block.

# shellcheck disable=SC1091
. ./common-setup.sh

count=0
failures=0
for vcs in git hg bzr brz src;
do
    if command -v "$vcs" >/dev/null
    then
	fail() {
	    echo "not ok - ${vcs} $*"
	    if [ -s /tmp/statusout$$ ]
	    then
		tapdump /tmp/statusout$$
	    fi
	}
	ignorecheck () {
	    # Take a pattern, a filename, a legend, and an exception
	    # regexp.  Stuff the pattern in the ignore file. Run the
	    # status command.  Success if the output is empty -
	    # nonempty with the nomatch option.
	    Z=!
	    N=
	    if [ "$1" = '--nomatch' ]
	    then
		Z=
		N=!
		shift
	    fi
	    pattern="$1"
	    # shellcheck disable=SC2034
	    match="$2"
	    legend="$3"
	    exceptions="$4"
	    count=$((count+1))
	    repository ignore
	    # shellcheck disable=SC2154
	    repository ignore "${ignorefile}"
	    repository ignore "${pattern}"
	    repository status >/tmp/statusout$$ 2>&1
	    if [ -n "${exceptions}" ] && expr "${vcs}" : "${exceptions}" >/dev/null
	    then
		# shellcheck disable=2057,2086
		if [ $Z -s "/tmp/statusout$$" ]; then failures=$((failures+1)); fail "${legend} unexpectedly succeeded"; fi
	    else
		# shellcheck disable=2057,2086
		if [ $N -s "/tmp/statusout$$" ]; then failures=$((failures+1)); fail "${legend} unexpectedly failed"; fi
	    fi
	}
	
	repository init $vcs /tmp/ignoretest$$
	case ${vcs} in
	    git|hg|bzr|brz|src)
		touch 'ignorable'
		# If this fails something very basic has gone wrong
		(repository status | grep '?[ 	]*ignorable' >/dev/null) || fail "${vcs} status didn't flag junk file"
		# The actual pattern tests start here.
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
		ignorecheck 'foo/bar' 'foo/bar' "check for exact match with /"
		# These tests fail because the git and hg status commands
		# do things that don't fit the test machinery's model.
		# We might be able to get somewhere with output trimming.
		if [ "${vcs}" != "hg" ] && [ "${vcs}" != "git" ] && [ "${vcs}" != "bzr" ] && [ "${vcs}" != "brz" ]
		then
		    ignorecheck --nomatch 'foo?bar' 'bar' "check for ? not matching /"
		    ignorecheck --nomatch '*bar' 'bar' "check for * not matching /"
		fi
		;;
	    *)
		echo "not ok -- no handler for $vcs"
		failures=$((failures+1))
	esac
	repository wrap
    else
        printf 'not ok: %s missing # SKIP\n' "$vcs"
	failures=$((failures+1))
    fi
    rm /tmp/statusout$$
done

echo "ok - ${failures} of ${count} ignore-pattern tests failed."

#end
