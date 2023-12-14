#!/bin/sh
# Boilerplate code begins.
#
# Not all platforms have a 'realpath' command, so fake one up if needed
# using $PWD.
# Note: GitLab's CI environment does not seem to define $PWD, however, it
# does have 'realpath', so use of $PWD here does no harm.
unset CDPATH	# See https://bosker.wordpress.com/2012/02/12/bash-scripters-beware-of-the-cdpath/

command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }

# Necessary so we can see reposurgeon and repocutter
PATH=$(realpath ..):$(realpath .):${PATH}

toolmeta() {
    stem=$(echo "$0" | sed -e 's/.sh//')
    case $1 in
       --regress)
	   if [ "$3" = "export" ]
	   then
               grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>/tmp/out$$
	   fi
           legend=$(sed -n '/^## /s///p' <"$0" 2>/dev/null);
           QUIET=${QUIET} ./tapdiffer <"$2" "${legend}" "${stem}.chk"; ;;
       --rebuild)
           cat "$2" >"${stem}.chk"
	   if [ "$3" = "export" ]
	   then
               grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>"${stem}.chk"
	   fi;;
       --view)
           cat "$2";;
       *)
           echo "toolmeta: unknown mode $1 in $0" >&2;; 
    esac
}

need () {
    set -- "$@" ""
    while [ -n "$1" ]; do
        command -v "$1" >/dev/null || set -- "$@" "$1"
        shift
    done
    shift
    if [ "$#" -gt 0 ]; then
        printf 'not ok: %s missing # SKIP\n' "$@"
        exit 0
    fi
}

tapcd () {
    cd "$1" >/dev/null || ( echo "not ok: $0: cd failed"; exit 1 )
}

svnwrap() {
    rm -fr test-repo$$ test-checkout$$
}

seecompare () {
    # Takes a test file on stdin. The arguments the arguments are the command
    trap 'rm -f /tmp/seecompare-before$$ /tmp/infile$$' EXIT HUP INT QUIT TERM
    cat >"/tmp/infile$$"
    # shellcheck disable=SC2086
    ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" see <"/tmp/infile$$" >/tmp/seecompare-before$$ 2>&1
    # shellcheck disable=SC2086,2048
    (${REPOCUTTER:-repocutter} -q -t "$(basename $0)" $* <"/tmp/infile$$" | repocutter -q -t "$(basename $0)" see >/tmp/seecompare-after$$) 2>&1
    diff --label Before --label After -u /tmp/seecompare-before$$ /tmp/seecompare-after$$
    exit 0
}

tapdump() {
    # Dump contents of argument files as a TAP YAML attachment
    echo "  --- |"
    # shellcheck disable=SC2068
    for f in $@
    do
	sed <"$f" -e 's/^/  /'
    done
    echo "  ..."
}

repository() {
    # Generic repository-manipulation code
    cmd="$1"
    shift
    case "${cmd}" in
	init)
	    # Initialize repo in specified temporary directory
	    #
	    # Always pair init and wrap calls.  The issue is that in
	    # SVN (and CVS, if and when when it's supported), the
	    # working directory where we need to create and manipulate
	    # files in between constructor actions is different from
	    # the sandbox root directory where the masters live (e.g
	    # where repository init is called).  init is going to put
	    # us into that directory; wrap backs us out.
	    #
	    repotype="$1"
	    rbasedir="/tmp/testrepo$$";
	    trap 'rm -fr ${rbasedir} /tmp/stream$$ /tmp/fossil$$ /tmp/addlist$$ /tmp/genout$$' EXIT HUP INT QUIT TERM
	    need "${repotype}"
	    rm -fr "${rbasedir}";
	    mkdir "${rbasedir}";
	    # shellcheck disable=SC2164
	    cd "${rbasedir}" >/dev/null || exit 1;
	    case "${repotype}" in
		bzr|brz|git|hg) "${repotype}" init -q;;
		fossil) fossil init /tmp/fossil$$ >/dev/null && fossil open /tmp/fossil$$ >/dev/null && mkdir .fossil-settings;;
		src) mkdir .src;;
		svn) svnadmin create .; svn co -q "file://$(pwd)" working-copy ; tapcd working-copy;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ts=10
	    fredname='Fred J. Foonly'
	    fredmail='fred@foonly.org'
	    fred="${fredname} <${fredmail}>"
	    # Make the ignore file available for ignore-pattern tests ignorefile=".${cmd}ignore"
	    ignorefile=".${repotype}ignore"
	    case "${repotype}" in
		brz) ignorefile=".bzrignore";;
		fossil) ignorefile=".fossil-settings/ignore-glob";;
	    esac
	    LF='
'
	    touch "/tmp/addlist$$"
	    ;;
	stdlayout)
	    case "${repotype}" in
		svn)
		    svn mkdir trunk && \
			svn mkdir tags && \
			svn mkdir branches && \
			echo "Standard directory layout." | svn commit -F - && \
			tapcd trunk
		    ;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	ignore)
	    # Clear or append to the ignore file. In Subversion, do the equivalent propset.
	    # Note: ignore calls are cumulative.
	    rpattern="$1"
	    if [ "${repotype}" = "svn" ]
	    then
		if [ -n "${rpattern}" ] && [ "${rpattern}" != '.svnignore' ]
		then
		   svn propset -q svn:ignore "${rpattern}" .
		fi
	    else
		if [ -z "${rpattern}" ]
		then
		    rm -f "${ignorefile}"
		else
		    echo "${rpattern}" | tr ' ' "${LF}" >>"${ignorefile}"
		fi
	    fi
	    ;;
	status)
	    # Get a one-per-line report of file status.  What we want from this is output
	    # that looks like svn status. Sometimes this involves filtering out junk lines,
	    # notably as issued for directories (since we're only interested in seeing if
	    # we can ignore files.)
	    case "${repotype}" in
		bzr|brz) "${repotype}" status -S | grep -v '/$';;
		fossil) fossil changes --all --extra --classify | sed '/^EXTRA/s//?/';;
		git) git status --porcelain -uall;;
		hg|src) "${repotype}" status;;
		svn) svn status | grep -v '  *M  *[.]';;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	commit)
	    # Add or commit content.  Changeset commit, not file
	    # commit.  If possible, force timestamp and author. When
	    # it isn't, that has to be cleaned up at export time.
	    file="$1"
	    comment="$2"
	    newcontent="$3"
	    if [ -z "${newcontent}" ]
	    then
		cat >"${file}"
	    else
		echo "${newcontent}" >"${file}"
	    fi
	    # Always do the add, ignore errors. Otherwise we'd have to check to see if
	    # the file is registered each time.
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    #rfc3339="$(date --date=@${ts} --rfc-3339=seconds | tr ' ' 'T')"

	    case "${repotype}" in
		bzr|brz)
		    grep "${file}" "/tmp/addlist$$" >/dev/null || { 
			"${repotype}" add "${file}" && echo "${file}" >>"/tmp/addlist$$"
		    }
		    # Doesn't force timestamps or committer.
		    "${repotype}" commit -m "${comment}${LF}" --author "${fred}"
		    ;;
		fossil)
		    grep "${file}" "/tmp/addlist$$" >/dev/null || { 
			fossil add "${file}" && echo "${file}" >>"/tmp/addlist$$"
		    }
		    # Doesn't force timestamps or author.  In theory
		    # this could be done with --date-override and
		    # --user-override, but that failed when it was tried.
		    fossil commit -m "${comment}"
		    ;;
		git)
		    grep "${file}" "/tmp/addlist$$" >/dev/null || { 
			git add "${file}" && echo "${file}" >>"/tmp/addlist$$"
		    }
		    # Git seems to reject timestamps with a leading zero
		    export GIT_COMMITTER="$fred}"
		    export GIT_AUTHOR="${fred}"
		    export GIT_COMMITTER_DATE="1${ft} +0000" 
		    export GIT_AUTHOR_DATE="1${ft} +0000" 
		    git commit -a -m "${comment}"
		    ;;
		hg)
		    grep "${file}" "/tmp/addlist$$" >/dev/null || { 
			 hg add "${file}" && echo "${file}" >>"/tmp/addlist$$"
		    }
		    # Untested.
		    # Doesn't force timestamps or author.
		    # Could be done with -d and -u
		    hg commit -m "${comment}"
		    ;;
		src)
		    # Doesn't force timestamps or author.
		    # Doesn't require an add if the file fails to exist.
		    src commit -m "${comment}"
		    ;;
		svn)
		    # shellcheck disable=SC2046,2086
		    if [ ! -d $(dirname ${file}) ]
		    then
			mkdir $(dirname ${file})
			svn add $(dirname ${file})
		    fi
		    grep "${file}" "/tmp/addlist$$" >/dev/null || { 
			svn add "${file}" && echo "${file}" >>"/tmp/addlist$$"
		    }
		    # Doesn't force timestamp or author.
		    svn commit -m "${comment}"
		    ;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	checkout)
	    # Checkout branch, creating if necessary.
	    branch="$1"
	    case "${repotype}" in
		git)
		    # shellcheck disable=SC2086
		    if [ "$(git branch | grep ${branch})" = "" ]
		    then
			git branch "${branch}"
		    fi
		    git checkout -q "$1";;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	merge)
	    # Perform merge with controlled clock tick.
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    case "${repotype}" in
		git)
		    export GIT_COMMITTER="$fred}"
		    export GIT_AUTHOR="${fred}"
		    export GIT_COMMITTER_DATE="1${ft} +0000" 
		    export GIT_AUTHOR_DATE="1${ft} +0000" 
		    git merge -q "$@";;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	mkdir)
	    # Make a directory or directories in the working copy
	    for d in "$@"
	    do
		mkdir -p "${d}"
		case "${repotype}" in
		    svn) svn add "${d}"; svn commit -m "${d} creation";;
		    *) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
		esac
	    done
	    ;;
	tag)
	    # Create lightweight tag.
	    tagname="$1"
	    case "${repotype}" in
		bzr|brz|git) "${repotype}" tag -q "${tagname}";;
		fossil) ;; # FIXME: Figure out how to make Fossil tags with Git lightweight tag behavior
		hg) hg tag "${tagname}";;
		src) src tag create "${tagname}";;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	atag)
	    # Create annotated tag
	    cat >"${file}"
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    case "${repotype}" in
		fossil)
		    # Untested, recorded so we don't have to rediscover argument magic later.
		    # Doesn't force timestamps or author.
		    # Could be done with --date-override and  --user-override.
		    # shellcheck disable=SC2086
		    fossil tag add "${tagname}" "$(fossil timeline -F "%H" | head -1)" "$(cat ${file})"
		    ;;
		git)
		    # Untested.
		    export GIT_COMMITTER="$fred}"
		    export GIT_COMMITTER_DATE="1${ft} +0000" 
		    git tag -a -F "${file}"
		    ;;
		hg)
		    # Untested.
		    # Doesn't force timestamps or author.
		    # Could be done with --d and  -u.
		    # shellcheck disable=SC2086
		    hg tag -m "${tagname}" "$(cat ${file})"
		    ;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	up)
	    # Update a checkout. Sometimes required to force a commit boundary
	    # (I'm looking at you, Subversion.)
	    case "${repotype}" in
		svn) svn -q up;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    ;;
	export)
	    # Dump export stream.  Clock-neutralize it if we were unable to force timestamps at commit time.
	    legend="$1"
	    neutralize() {
		tt=10
		while read -r line;
		do
		    # shellcheck disable=SC2086
		    set -- $line
		    case "$1" in
			commit) tt=$((tt + 60)); echo "${line}";;
			committer) echo "committer ${fredname} <${fredmail}> ${tt} +0000";;
			author) echo "author ${fredname} <${fredmail}> ${tt} +0000";;
			tagger) echo "tagger ${fredname} <${fredmail}> ${tt} +0000";;
			*) echo "$line" ;;
		    esac
		done 
	    }
	    case "${repotype}" in
		bzr|brz) "${repotype}" fast-export -q | neutralize >/tmp/stream$$;;
		fossil) fossil export --git | neutralize >/tmp/stream$$;;
		git) git fast-export --all >/tmp/stream$$;;
		hg)
		    need hg-fast-export.py
		    # https://github.com/frej/fast-export
		    # This doesn't work yet. It dies with a message about a missing hg2git module.
		    hg-fast-export.py | neutralize >/tmp/stream$$
		    ;;
		src)
		    src fast-export | neutralize >/tmp/stream$$
		    ;;
		svn)
		    (tapcd "${rbasedir}"	# Back to the repository root
		     # shellcheck disable=SC1117,SC1004,SC2006,SC2086
		     svnadmin dump -q "." | repocutter -q -t "${rbasedir}" testify) >/tmp/stream$$
		    ;;
		*) echo "not ok - ${cmd} under ${repotype} not supported in repository shell function."; exit 1;;
	    esac
	    case "${repotype}" in
		svn)
		    # shellcheck disable=SC1117,SC1004,SC2006,SC2086
		    cat /tmp/stream$$ | sed "1a\
\ ## ${legend}
" | sed "2a\
\ # Generated - do not hand-hack!
"
		    ;;
		*)
		    echo "## ${legend}"; echo "# Generated - do not hand-hack!"; cat /tmp/stream$$;
		    ;;
	    esac
	    
	    ;;
	wrap)
	    # We're done. Make sure we've returned to the sandbox root.
	    case "${repotype}" in
		svn) tapcd "${rbasedir}";;	# Back out of the working directory
	    esac
	    ;;
	*)
	    echo "not ok - ${cmd} is not a command in the repository shell function.";
	    exit 1
	    ;;
    esac
}

# Boilerplate ends 
