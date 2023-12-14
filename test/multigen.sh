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

rm -f /tmp/genout$$
outsink=/dev/stdout
msgsink=/dev/null
while getopts o:v opt
do
    case $opt in
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

    # Content operations start here
    vc mkdir project1/
    vc mkdir project1/trunk/
    vc mkdir project1/branches/
    vc mkdir project1/tags/
    vc commit "project1/trunk/foo.txt" "Example content" "Now is the time."
    vc commit "project1/trunk/bar.txt" "Example content in different file"  "For all good men."
    vc commit "project1/trunk/baz.txt" "And in yet another file" "to come to the aid of their country."
    svn up  # Without this, the next copy does file copies.  With it, a directory copy. 
    svn copy project1/trunk project1/branches/stable
    svn commit -m "First directory copy"
    vc mkdir project2/
    vc mkdir project2/trunk/
    vc mkdir project2/branches/
    vc mkdir project2/tags/
    vc commit "project2/trunk/foo.txt" "Hamlet the Dane said this" "Whether tis nobler in the mind."
    vc commit "project2/trunk/bar.txt" "He continued" "or to take arms against a sea of troubles"
    vc commit "project2/trunk/baz.txt" "The build-up" "and by opposing end them"
    vc commit "project2/trunk/foo.txt" "Famous soliloquy begins" "to be," 
    vc commit "project2/trunk/foo.txt" "And continues" "or not to be."
    svn up
    # This is a remnant of our older way of writing Subversion
    # generators. No point in disturbing it.  The vc finctions wouild
    # insert a commit after the directory add.
    mkdir project2/trunk/foodir
    svn add project2/trunk/foodir
    echo "He was born with the gift of laughter" >project2/trunk/foodir/qux.txt
    svn add project2/trunk/foodir/qux.txt
    svn commit -m "and a sense that the world is mad."
    # End of old-style stuff
    svn up
    svn copy project2/trunk project2/tags/1.0
    svn commit -m "First tag copy"
    svn copy project2/trunk project1/trunk/evilcopy
    svn commit -m "Example cross-project copy"
    vc mkdir project3/
    vc mkdir project3/trunk/
    vc mkdir project3/branches/
    vc mkdir project3/tags/
    vc commit "project3/trunk/foo.txt" "I learned to relish the beauty of manners" "From my grandfather Verus"
    vc commit "project3/trunk/bar.txt" "From the fame and character my father obtain'd" "and to restrain all anger."
    vc commit "project3/trunk/baz.txt" "Of my mother;" "modesty, and a many deportment."
    vc commit "project3/trunk/foo.txt" "and to guard not only against evil actions," "I learned to be religuious and liberal;" 
    vc commit "project3/trunk/foo.txt" "entering my thoughts" "but even against any evil intentions"
    svn up
    # Write a span of per-project trunk-to-branch copies that needs to be coalesced by swapsvn
    # Ideally these should turn into a single copy trunk/ branches/sample
    svn copy project1/trunk project1/branches/sample
    svn commit -m "Create sample branch of project1"
    # End of old-style stuff
    svn up
    svn copy project2/trunk project2/branches/sample
    svn commit -m "Create sample branch of project2"
    svn up
    svn copy project3/trunk project3/branches/sample
    svn commit -m "Create sample branch of project3"
    svn up
    # Can't use vc commit here, it fails trying to do an add
    # because it doesn't avoid paths created by copy opertions.
    echo "Fourscore and seven years ago" >project3/branches/sample/foo.txt
    svn commit -m "Gettysburg speech begin"
    # End of old-style stuff
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
    # Pathological copy that needs to be split.  We're copying project1
    # because it has a branch that should be caught by wildcarding.
    svn copy project1 project4
    svn commit -m "Should become 3 copies of project3/{trunk,branches,tags}"
    svn up
    vc wrap
} >"${msgsink}" 2>&1
vc export "Multi-project repository example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
