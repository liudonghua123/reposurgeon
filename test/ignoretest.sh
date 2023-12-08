#!/bin/sh
## Test ignore features
#
# Outputs one line of TAP on success.  On failures, multiple TAP lines
# each with the offending status-command dump following as a YAML
# block.  On failure this violates the assumption in the Makefile that there
# is only one test point per test failure, so test enumeration for purposes
# of checking plan underrun and overrun will be compromised. 
#
# The reason testing CVS isn't supported is that there's no way to get
# from it a tabular status command reporting on each file in your
# working directory. And if there were such a thing, it turns out
# ignore patterns only apply to the commands "update", "import" and
# release; an ifgnored files's CVD status report does not change.

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
With -d, dump capabilities as a YAML block after success.
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
	    # nonempty with the --nonempty option.
	    Z=!
	    N=
	    if [ "$1" = '--nonempty' ]
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
		ignorecheck 'ignora?le' 'ignorable' "check for ? wildcard" "hg"	QUES
		ignorecheck 'ignorab[klm]e' 'ignorable' "check for range syntax"
		ignorecheck 'ignorab[k-m]e' 'ignorable' "check for dash in ranges"
		ignorecheck 'ignorab[!x-z]e' 'ignorable' "check for !-negated ranges" "hg" BANG
		ignorecheck 'ignorab[^x-z]e' 'ignorable' "check for ^-negated ranges" "src" CARET
		ignorecheck --nonempty '\*' 'ignorable' "check for backslash escaping" "bzr|brz" ESC
		ignorecheck --nonempty 'ign* !ignorable' 'ignorable' "check for prefix negation" "hg" NEG
		rm ignorable
		touch .alpha
		ignorecheck --nonempty '[.]alpha' '.alpha' "explicit-leading-dot required" "git|svn|hg|bzr|brz" FNMDOT
		rm .alpha
		mkdir foo
		touch foo/bar
		if [ "${vcs}" != 'svn' ]
		then
		    # Strange failure - should investigate further.
		    ignorecheck 'foo/bar' 'foo/bar' "check for exact match with /"
		fi
		ignorecheck --nonempty 'foo?bar' 'bar' "check for ? not matching /" "bzr|brz"
		ignorecheck --nonempty 'fo*bar' 'bar' "check for * not matching /" "bzr|brz" FNMPATH
		rm foo/bar
		touch foo/subignorable
		ignorecheck 'subignorable' 'subignorable' "check for subdirectory match" "svn|src" LOOSE
		rm foo/subignorable
		mkdir -p foo/x/y
		touch foo/x/y/bar
		ignorecheck 'foo/**/bar' 'bar' "check ** wildcard" "svn|hg|src" DSTAR
		ignorecheck --nonempty 'y/bar' 'bar' "check whether / forces anchoring" "hg|bzr|brz" ASLASH
		ignorecheck 'foo/x' 'bar' "check whether directory match is a wildcard" "svn|src" DIRMATCH
		rm -fr foo
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
