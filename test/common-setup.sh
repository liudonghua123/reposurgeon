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

# Initialize a Subversion test repository with standard layout
svninit() {
        echo "Starting at ${PWD}"
 	# Note: this leaves you with the checkout directory current
    	svnadmin create test-repo$$
	    svn co "file://$(pwd)/test-repo$$" test-checkout$$ && \
	    cd test-checkout$$ >/dev/null && \
	    svn mkdir trunk && \
	    svn mkdir tags && \
	    svn mkdir branches && \
	    echo "Directory layout." | svn commit -F - && \
	    echo "This is a test Subversion repository" >trunk/README && \
	    svn add trunk/README && \
	    echo "Initial README content." | svn commit -F -
}

# Initialize a Subversion test repository with flat layout
svnflat() {
	svnadmin create test-repo
	svn co "file://$(pwd)/test-repo" test-checkout
}

svnaction() {
    # This version of svnaction does filenames or directories 
    case $1 in
	*/)
	    directory=$1
	    comment=${2:-$1 creation}
	    if [ ! -d "$directory" ]
	    then
		mkdir "$directory"
		svn add "$directory"
	    fi
	    svn commit -m "$comment"
	;;
	*)
	    filename=$1
	    content=$2
	    comment=$3
	    # shellcheck disable=SC2046
	    if [ ! -f "$filename" ]
	    then
		if [ ! -d $(dirname "$filename") ]
		then
		    mkdir $(dirname "$filename")
		    svn add $(dirname "$filename")
		fi
		echo "$content" >"$filename"
		svn add "$filename"
	    else
		echo "$content" >"$filename"
	    fi
	    svn commit -m "$comment"
	;;
    esac
}

svndump() {
    # shellcheck disable=SC1117,SC1004,SC2006,SC2086
    svnadmin dump -q "$1" | repocutter -q -t "$(basename $0)" testify | sed "1a\
\ ## $2
" | sed "2a\
\ # Generated - do not hand-hack!
"
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

repository() {
    # Generic repository constructor
    cmd="$1"
    shift
    case "${cmd}" in
	init)
	    # Initialize repo in specified temporary directory
	    repotype="$1"	# Not yet nontrivially used
	    base="$2";
	    need "${repotype}"
	    rm -fr "${base}";
	    mkdir "${base}";
	    # shellcheck disable=SC2164
	    cd "${base}" >/dev/null || exit 1;
	    case "${repotype}" in
		git|bzr|brz) "${repotype}" init -q;;
		svn) svnadmin create .; svn co -q "file://$(pwd)" checkout ;;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    ts=10
	    ;;
	commit)
	    # Add or commit content
	    file="$1"
	    text="$2"
	    if [ -d "checkout" ]; then cd checkout >/dev/null || exit 1; fi
	    cat >"${file}"
	    # Always do the add, ignore errors. Otherwise we'd have to check to see if
	    # the file is registered each time.
	    "${repotype}" add -q "${file}" >/dev/null 2>&1 || :
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    LF='
'
	    case "${repotype}" in
		git)
		    # Git seems to reject timestamps with a leading zero
		    export GIT_COMMITTER_DATE="1${ft} +0000" 
		    export GIT_AUTHOR_DATE="1${ft} +0000" 
		    git commit -q -a -m "${text}" --author "Fred J. Foonly <fred@foonly.org>";;
		bzr|brz)
		    # Doesn't force timestamps.
		    "${repotype}" commit -q -m "${text}${LF}" --author "Fred J. Foonly <fred@foonly.org>";;
		svn)
		    # Doesn't force timestamp or author.
		    svn commit -q -m "${text}";;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    # shellcheck disable=SC2046,2086
	    if [ $(basename $(pwd)) = "checkout" ]; then cd .. >/dev/null || exit 1; fi
	    ;;
	checkout)
	    # Checkout branch, creating if necessary
	    branch="$1"
	    case "${repotype}" in
		git)
		    # shellcheck disable=SC2086
		    if [ "$(git branch | grep ${branch})" = "" ]
		    then
			git branch "${branch}"
		    fi
		    git checkout -q "$1";;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    ;;
	merge)
	    # Perform merge with controlled clock tick.
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    case "${repotype}" in
		git)
		    export GIT_COMMITTER_DATE="1${ft} +0000" 
		    export GIT_AUTHOR_DATE="1${ft} +0000" 
		    git merge -q "$@";;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    ;;
	mkdir)
	    # Make a directory or directories in the checkout
	    if [ -d "checkout" ]; then cd checkout >/dev/null || exit 1; fi
	    for d in "$@"
	    do
		mkdir -p "${d}"
		case "${repotype}" in
		    svn) svn add -q "${d}"; svn commit -q -m "${d} creation";;
		esac
	    done
	    # shellcheck disable=SC2046,2086
	    if [ $(basename $(pwd)) = "checkout" ]; then cd .. >/dev/null || exit 1; fi
	    ;;
	tag)
	    # Create a (lightweight) tag
	    tagname="$1"
	    case "${repotype}" in
		git|bzr|brz) "${repotype}" tag -q "${tagname}";;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    ;;
	export)
	    # Dump export stream.  Clock-neutralize it if we were unable to force timestamps at commit time
	    trap 'rm -f /tmp/stream$$' EXIT HUP INT QUIT TERM
	    case "${repotype}" in
		git) git fast-export -q --all >/tmp/streamm$$;;
		bzr|brz) "${repotype}" fast-export -q | reposurgeon "read -" "timequake --tick Fred J. Foonly <fred@foonly.org>" "write >/tmp/stream$$";;
		svn)
		    spacer=' '
		    # shellcheck disable=SC1117,SC1004,SC2006,SC2086
		    svnadmin dump -q "." | repocutter -q -t "${base}" testify >/tmp/stream$$
		    ;;
		*) echo "not ok - ${cmd} not supported in repository shell function"; exit 1;;
	    esac
	    echo "${spacer}## $1"; echo "${spacer}# Generated - do not hand-hack!"; cat /tmp/stream$$
	    ;;
    esac
}

# Boilerplate ends 
