#!/bin/sh
## Test repocutter filecopy resolution

stream=n
cleanup=y
while getopts nr opt
do
    case $opt in
    n) stream=False ; cleanup=False ;;
    r) stream=True  ; cleanup=False ;;
    *) echo "$0: unknown option $opt"; exit 1;;
    esac
done

if [ "$cleanup" = "y" ]
then
    trap 'rm -f /tmp/resolved$$' EXIT HUP INT QUIT TERM
fi

${REPOCUTTER:-repocutter} -q -r 5:6 filecopy <filecopy.svn >/tmp/resolved$$.svn

if [ "$stream" = "y" ]
then
    cat /tmp/resolved$$.svn
else
    diff --label Before --label After -u filecopy.svn /tmp/resolved$$.svn
    # This should have no output
    ${REPOSURGEON:-reposurgeon} "read </tmp/resolved$$.svn"
fi

exit 0

