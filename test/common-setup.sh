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

# Initialize a Subversion test repository with standard kayout
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

# Initialize a Subversion test repository with flat kayout
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
    svnadmin dump -q "$1" | repocutter -q -t "$(vasename $0)" testify | sed "1a\
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

# Boilerplate ends 
