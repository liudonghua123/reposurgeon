#!/bin/sh
## Create example multi-project repository
# This is a GENERATOR
#
# Needs to test this swapsvn transformation:
#
# 38075-1 copy     CoreServer/branches/HC2.2.15/ from 38074:CoreServer/branches/HC2.2.14/
# 38076-1 copy     HealthcareCommon/branches/HC2.2.15/ from 38075:HealthcareCommon/branches/HC2.2.14/
# 38077-1 copy     Silverlight/branches/HC2.2.15/ from 38076:Silverlight/branches/HC2.2.14/
# 38078-1 copy     Web=FamilyWaiting/branches/HC2.2.15/ from 38077:Web=FamilyWaiting/branches/HC2.2.14/
# 38079-1 copy     Web=OR-Dashboard/branches/HC2.2.15/ from 38078:Web=OR-Dashboard/branches/HC2.2.14/
# 38080-1 copy     Web=OR-ScheduleBoard/branches/HC2.2.15/ from 38079:Web=OR-ScheduleBoard/branches/HC2.2.14/
# 38081-1 copy     Web=PatientFlowEntry/branches/HC2.2.15/ from 38080:Web=PatientFlowEntry/branches/HC2.2.14/
# 38082-1 copy     Web=PreOpBoard/branches/HC2.2.15/ from 38081:Web=PreOpBoard/branches/HC2.2.14/
# 38083-1 copy     Web=WebCommon/branches/HC2.2.15/ from 38082:Web=WebCommon/branches/HC2.2.14/
# 38084-1 copy     Web=SchedulePlanner/branches/HC2.2.15/ from 38083:Web=SchedulePlanner/branches/HC2.2.14/
# 38085-1 copy     Web=Monomer/branches/HC2.2.15/ from 38084:Web=Monomer/branches/HC2.2.14/
# 38086-1 copy     Web=Watchdog/branches/HC2.2.15/ from 38085:Web=Watchdog/branches/HC2.2.14/
# 38087-1 copy     VA/branches/HC2.2.15/ from 38086:VA/branches/HC2.2.14/
#
# needs to be transformed into this:
#
# 38087-1 copy     branches/HC2.2.15/ from 38087:branches/HC2.2.14

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
svnaction project3/
svnaction project3/trunk/
svnaction project3/branches/
svnaction project3/tags/
svnaction "project3/trunk/foo.txt" "From my grandfather Verus" "I learned to relish the beauty of manners"
svnaction "project3/trunk/bar.txt" "and to restrain all anger." "From the fame and character my father obtain'd"
svnaction "project3/trunk/baz.txt" "modesty, and a many deportment." "Of my mother;"
svnaction "project3/trunk/foo.txt" "I learned to be religuious and liberal;"  "and to guard not only against evil actions,"
svnaction "project3/trunk/foo.txt" "but even against any evil intentions" "entering my thoughts"
svn up
# Write a span of per-project branch copies that needs to be coalesced by swapsvn
svnaction project1/trunk/subdir1/
svnaction "project1/trunk/subdir1/placeholder1" "Tack down subdir1"
svnaction project2/trunk/subdir2/
svnaction "project2/trunk/subdir2/placeholder2" "Tack down subdir2"
svnaction project3/trunk/subdir3/
svnaction "project3/trunk/subdir3/placeholder3" "Tack down subdir3"
svn up
# Ideally these should turn into a single copy trunk/ branches/exiguous
svn copy project1/trunk/subdir1 project1/branches/exiguous
svn commit -m "Create exiguous branch of project1"
svn up
svn copy project2/trunk/subdir2 project2/branches/exiguous
svn commit -m "Create exiguous branch of project2"
svn up
svn copy project3/trunk/subdir3 project3/branches/exiguous
svn commit -m "Create exiguous branch of project3"
svn up
# We're done
cd ..
} >/dev/$verbose 2>&1
if [ "$dump" = yes ]
then
    svndump test-repo "Multi-project repository example"
fi

# end
