#!/bin/sh
## General test load for ancestry-chasing logic
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

rm -f /tmp/genout$$
outsink=/dev/stdout
msgsink=/dev/null
while getopts do:v opt
do
    case $opt in
	d) ;;
	o) outsink=/tmp/genout$$; target="${OPTARG}";;
	v) msgsink=/dev/stdout; outsink=/dev/null;;
	*) echo "$0: unknown flag $opt" >&2; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

here=$(pwd)
{
    vc init svn
    vc stdlayout
    tapcd ..
    # Content operations start here
    vc commit "trunk/foo.txt" "More example content"  "Now is the time."
    vc commit "trunk/bar.txt" "Example content in different file"  "For all good men."
    vc commit "trunk/baz.txt" "And in yet another file" "to come to the aid of their country."
    svn up  # Without this, the next copy does file copies.  With it, a directory copy. 
    svn copy trunk branches/stable
    svn commit -m "First directory copy"
    vc commit "trunk/foo.txt" "Hamlet the Dane said this" "Whether tis nobler in the mind."
    vc commit "trunk/bar.txt" "He continued" "or to take arms against a sea of troubles"
    vc commit "trunk/baz.txt" "The build-up" "and by opposing end them"
    vc commit "trunk/foo.txt" "Famous soliloquy begins" "to be,"
    vc commit "branches/foo.txt" "And continues" "or not to be."
    svn up
    svn copy trunk tags/1.0
    svn commit -m "First tag copy"
    vc wrap
} >"${msgsink}" 2>&1
vc export "ancestry-chasing test" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
