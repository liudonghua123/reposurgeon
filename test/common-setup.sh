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
	   if [ "$3" = "export" ]
	   then
               grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>/tmp/out$$
	   fi
           legend=$(sed -n '/^## /s///p' <"$0" 2>/dev/null);
           QUIET=${QUIET} ./tapdiffer <"$2" "${legend}" "${stem}.chk"; ;;
       --rebuild)
           cat "$2" >"$(stem).chk"
	   if [ "$3" = "export" ]
	   then
               grep "^done" /tmp/out$$ >/dev/null 2>&1 || echo "done" >>"${stem.chk}"
	   fi;;
       --view)
           cat "$2";;
       *)
           echo "toolmeta: unknown mode $1 in $0" >&2;; 
    esac
}

need () {
    # shellcheck disable=SC2068
    for tool in $@
    do
	command -v "${tool}" >/dev/null 2>&1 || ( echo "not ok: ${tool} missing. | SKIP"; exit 0; )
    done
}

tapcd () {
    cd "$1" >/dev/null || ( echo "not ok: $0: cd failed"; exit 1 )
}

# Boilerplate ends 
