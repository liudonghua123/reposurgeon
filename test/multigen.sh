#!/bin/sh
## Create example multi-project repository
# This is a GENERATOR

dump=no
verbose=null
while getopts dv opt
do
    case $opt in
	d) dump=yes;;
	v) verbose=stdout;;
	*) echo "$0: unknown flag $opt" >&2; exit 1;;
    esac
done

# shellcheck disable=SC1091
. ./common-setup.sh

trap 'rm -fr test-repo test-checkout' EXIT HUP INT QUIT TERM 

{
set -e
make svn-flat
cd test-checkout
# Content operations start here
svnaction project1/
svnaction project1/trunk/
svnaction project1/branches/
svnaction project1/tags/
svnaction "project1/trunk/foo.txt" "Now is the time." "Example content" 
svnaction "project1/trunk/bar.txt" "For all good men." "Example content in different file" 
svnaction "project1/trunk/baz.txt" "to come to the aid of their country." "And in yet another file"
svn up  # Without this, the next copy does file copies.  With it, a directory copy. 
svn copy project1/trunk project1/branches/stable
svn commit -m "First directory copy"
svnaction project2/
svnaction project2/trunk/
svnaction project2/branches/
svnaction project2/tags/
svnaction "project2/trunk/foo.txt" "Whether tis nobler in the mind." "Hamlet the Dane said this"
svnaction "project2/trunk/bar.txt" "or to take arms against a sea of troubles" "He continued"
svnaction "project2/trunk/baz.txt" "and by opposing end them" "The build-up"
svnaction "project2/trunk/foo.txt" "to be,"  "Famous soliloquy begins"
svnaction "project2/trunk/foo.txt" "or not to be." "And continues"
svn up
svn copy project2/trunk project2/tags/1.0
svn commit -m "First tag copy"
svn copy project2/trunk project1/trunk/evilcopy
svn commit -m "Example cross-project copy"
# We're done
cd ..
} >/dev/$verbose 2>&1
if [ "$dump" = yes ]
then
    svndump test-repo "Multi-project repository example"
fi

# end
