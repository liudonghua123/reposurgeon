#!/bin/sh
## Test repocutter filecopy resolution

stream=False
cleanup=True
while getopts d:nr opt
do
    case $opt in
    d) DEBUGOPT="-d $OPTARG";;
    n) stream=False ; cleanup=False ;;
    r) stream=True  ; cleanup=False ;;
    *) echo "$0: unknown option $opt"; exit 1;;
    esac
done

if [ "$cleanup" = True ]
then
    trap 'rm -f /tmp/resolved$$' EXIT HUP INT QUIT TERM
fi

# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} ${DEBUGOPT} -q -r 5:6 filecopy <filecopy.svn >/tmp/resolved$$.svn

if [ "$stream" = True ]
then
    cat /tmp/resolved$$.svn
else
    diff --label Before --label After -u filecopy.svn /tmp/resolved$$.svn
    # This should have no output
    ${REPOSURGEON:-reposurgeon} "read </tmp/resolved$$.svn"
fi

exit 0

