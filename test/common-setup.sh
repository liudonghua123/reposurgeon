#!/bin/sh
# Boilerplate code begins.
#
# Not all platforms have a 'realpath' command, so fake one up if needed
# using $PWD.
# Note: GitLab's CI environment does not seem to define $PWD, however, it
# does have 'realpath', so use of $PWD here does no harm.
command -v realpath >/dev/null 2>&1 ||
    realpath() { test -z "${1%%/*}" && echo "$1" || echo "$PWD/${1#./}"; }

toolmeta() {
    stem=$(echo "$0" | sed -e 's/.sh//')
    case $1 in
       --regress)
           # This line is a kludge to deal with the fact that the git version
           # running the tests may be old enough to not DTRT
           #grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>/tmp/out$$
	   diff --text -u "${stem}.chk" /tmp/out$$ || ( echo "$0: FAILED"; exit 1 ); ;;
       --rebuild)
           # grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>/tmp/out$$
           cat "$2" >"$(stem).chk";;
       --view)
           cat "$2";;
       *)
           echo "toolmeta: unknown mode $1 in $0" >&2;; 
    esac
}

# Boilerplate ends 
