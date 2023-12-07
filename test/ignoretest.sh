#!/bin/sh
## Test ignore features
#
# Outputs one line of TAP on success.  On failures, muktiple TAP lines
# each with the offending status-command dump following as a YAML
# block.

systems="git svn hg bzr brz src"
verbose=no
restrict=""
flagdump=no
while getopts dr:s:v opt
do
    case $opt in
	d) flagdump=yes;;
	r) restrict="$OPTARG";;
	s) systems="$OPTARG";;
	v) verbose=yes;;
	*) cat <<EOF
ignoretest.sh - examine ignore-pattern behavior

With -r, restrict tisting to the specified pattern.
With -s, set the VCSes tested.
With -v, run in verbose mode, dumping test output.
With -d, dump capabilities as a YAML block after success/
EOF
	   exit 0;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

# shellcheck disable=SC1091
. ./common-setup.sh

count=0
failures=0
rm -f /tmp/statusout$$
for vcs in ${systems};
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
	    flag="$5"

	    if [ -n "${restrict}" ] && [ "${restrict}" != "${pattern}" ]
	    then
		return
	    fi

	    if [ "${verbose}" = "yes" ]
	    then
		echo "Test: '${vcs}, ${pattern}' for '${legend}', exceptions '${exceptions}'"
	    fi

	    count=$((count+1))
	    repository ignore
	    # shellcheck disable=SC2154
	    repository ignore "${ignorefile}"
	    repository ignore "${pattern}"
	    repository status >/tmp/statusout$$ 2>&1
	    
	    if [ -n "${exceptions}" ] && (echo "${vcs}" | grep -E "${exceptions}" >/dev/null)
	    then
		# shellcheck disable=2057,2086
		if [ $Z -s "/tmp/statusout$$" ];
		then
		    failures=$((failures+1));
		    fail "${legend} unexpectedly succeeded";
		fi
	    else
		# shellcheck disable=2057,2086
		if [ $N -s "/tmp/statusout$$" ];
		then
		    failures=$((failures+1));
		    fail "${legend} unexpectedly failed";
		elif [ -n "${flag}" ]
		then
		    printf "\t%s" "${flag}" >>/tmp/ignoretable$$
		fi
	    fi
	}
	
	repository init "$vcs" /tmp/ignoretest$$
	case ${vcs} in
	    git|hg|bzr|brz|src|svn)
		printf "%s: " "${vcs}" >>/tmp/ignoretable$$
		touch 'ignorable'
		# If this fails something very basic has gone wrong
		(repository status | grep '?[ 	]*ignorable' >/dev/null) || fail "status didn't flag junk file"
		# The actual pattern tests start here.
		ignorecheck 'ignorable' 'ignorable' "basic ignore"
		ignorecheck 'ignor*' 'ignorable' "check for * wildcard"
		ignorecheck 'ignora?le' 'ignorable' "check for ? wildcard" "hg"	QUESTION
		ignorecheck 'ignorab[klm]e' 'ignorable' "check for range syntax"
		ignorecheck 'ignorab[k-m]e' 'ignorable' "check for dash in ranges"
		ignorecheck 'ignorab[!x-z]e' 'ignorable' "check for !-negated ranges" "hg" BANGDASH
		ignorecheck 'ignorab[^x-z]e' 'ignorable' "check for ^-negated ranges" "src" CARETDASH
		ignorecheck --nomatch '\*' 'ignorable' "check for backslash escaping" "bzr|brz"	BACKSLASH
		ignorecheck --nomatch 'ign* !ignorable' 'ignorable' "check for prefix negation"	"hg" NEGATION
		rm ignorable
		mkdir foo
		touch foo/bar
		if [ "${vcs}" != 'svn' ]
		then
		    # Strange failure - should investigate further.
		    ignorecheck 'foo/bar' 'foo/bar' "check for exact match with /"
		fi
		ignorecheck --nomatch 'foo?bar' 'bar' "check for ? not matching /" "bzr|brz"
		ignorecheck --nomatch 'fo*bar' 'bar' "check for * not matching /" "bzr|brz" FNMPATHNAME
		rm foo/bar
		touch foo/subignorable
		ignorecheck 'subignorable' 'subignorable' "check for subdirectory match" "svn|src" RECURSIVE
		rm foo/subignorable
		rmdir foo
		printf "\n" >>/tmp/ignoretable$$
		;;
	    *)
		echo "Bail out! No handler for ${vcs}"
		failures=$((failures+1))
		exit 1
	esac
	repository wrap
    else
        printf 'not ok - %s missing # SKIP\n' "$vcs"
	failures=$((failures+1))
    fi
    rm -f /tmp/statusout$$
done

if [ "${failures}" = "0" ]
then
    echo "ok - ${count} ignore-pattern tests succeeded."
    if [ "${flagdump}" = yes ]
    then
	tapdump /tmp/ignoretable$$
    fi
else
    echo "not ok - ${failures} of ${count} ignore-pattern tests failed."
fi
rm -f  /tmp/ignoretable$$

#end
