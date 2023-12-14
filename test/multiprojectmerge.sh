#!/bin/sh
# Generate an SVN dump of multiple projects that are later merged into a single trunk
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
    repository init svn

    projects="software firmware docs"

    # r1
    for project in $projects; do
	for dir in trunk branches tags
	do
	    mkdir -p "$project/$dir"
	done
	svn add "$project"
    done
    svn commit -m 'init multi-project repo'

    # r2, r3, r4
    for project in $projects; do
	echo "initial $project content" >"$project/trunk/$project.txt"
	svn add "$project/trunk/$project.txt"
	svn commit -m "initial $project commit"
    done

    # If you add intermediate commits here (and adjust `merge` commands accordingly), no invalid import stream is generated.
    # So the bug seems to only affect merges from root commits.
    # for project in $projects; do
    #     echo "some early changes on $project" >>$project/trunk/$project.txt
    #     svn commit -m "second $project commit"
    # done

    # r5
    mkdir trunk branches tags
    svn add trunk branches tags
    svn commit -m "We don't want to develop separate projects anymore! Prepare for one single trunk."

    # r6, r7, r8
    for project in $projects; do
	svn copy "$project/trunk" "trunk/$project"
	svn commit -m "copy $project to new trunk"
    done
    svn up

    #r9
    for project in $projects; do
	echo "continue $project development" >>"trunk/$project/$project.txt"
    done
    svn commit -m "continue development on new trunk"

    repository wrap
} >"${msgsink}" 2>&1
repository export "multiple projects merged into common trunk example" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

# end
