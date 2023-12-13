#!/bin/sh
## Create example multi-project repository
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
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

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

{
    repository init svn

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
    svnaction "project2/trunk/foodir/qux.txt" "He was born with the gift of laughter" "and a sense that the world is mad."
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
    # Write a span of per-project trunk-to-branch copies that needs to be coalesced by swapsvn
    # Ideally these should turn into a single copy trunk/ branches/sample
    svn copy project1/trunk project1/branches/sample
    svn commit -m "Create sample branch of project1"
    svn up
    svn copy project2/trunk project2/branches/sample
    svn commit -m "Create sample branch of project2"
    svn up
    svn copy project3/trunk project3/branches/sample
    svn commit -m "Create sample branch of project3"
    svn up
    svnaction "project3/branches/sample/foo.txt" "Fourscore and seven years ago"
    svn up
    svn copy project2/trunk/foodir project3/branches/sample
    svn commit -m "Copy after branch creation"
    svn up
    # Now we're going to do a branch to branch copy
    svn copy project1/branches/sample project1/branches/sample2
    svn commit -m "Copy sample branch of project1"
    svn up
    svn copy project2/branches/sample project2/branches/sample2
    svn commit -m "Copy sample branch of project2"
    svn up
    svn copy project3/branches/sample project3/branches/sample2
    svn commit -m "Copy sample branch of project3"
    svn up
    # Test (absence of) delete coalescence
    svn delete project1/branches/sample
    svn commit -m "Delete sample branch of project1"
    svn up
    svn delete project2/branches/sample
    svn commit -m "Delete sample branch of project2"
    svn up
    svn delete project3/branches/sample
    svn commit -m "Delete sample branch of project3"
    svn up
    # Test that handling of second coalescence clique is correct
    svn copy project1/trunk project1/branches/sample3
    svn commit -m "Create sample3 branch of project1"
    svn up
    svn copy project2/trunk project2/branches/sample3
    svn commit -m "Create sample3 branch of project2"
    svn up
    svn copy project3/trunk project3/branches/sample3
    svn commit -m "Create sample3 branch of project3"
    svn up
    # Test rename coalescence
    svn rename project1/branches/sample3 project1/branches/renamed
    svn commit -m "Rename sample3 branch of project1"
    svn up
    svn rename project2/branches/sample3 project2/branches/renamed
    svn commit -m "Rename sample3 branch of project2"
    svn up
    svn rename project3/branches/sample3 project3/branches/renamed
    svn commit -m "Rename sample3 branch of project3"
    svn up
    # Pathological copy that needs to be split.  We're cipying project1
    # because it has a branch that should be caught by wildcarding.
    svn copy project1 project4
    svn commit -m "Should become 3 copies of project3/{trunk,branches,tags}"
    svn up
    repository wrap
} >/dev/$verbose 2>&1

if [ "$dump" = yes ]
then
    repository export "Multi-project repository example"
fi

# end
