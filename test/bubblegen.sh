#! /bin/sh
# Create a small test repository with a merge bubble
#
# Based on the merge example at
# https://git-scm.com/book/en/v2/Git-Branching-Basic-Branching-and-Merging
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
    vc init git

    #C1
    vc commit sample "First commit (master)" <<EOF
First line of sample content.
EOF

    #C1
    vc commit sample "Second commit (master)" <<EOF
First line of sample content.
Second line of sample content.
EOF

    #C2
    vc commit sample "Third commit (master)" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

    vc checkout iss53

    #C3
    vc commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
EOF

    vc checkout master

    vc checkout hotfix

    #C4
    vc commit sample3 "Fix broken email address" <<EOF
First line of sample3 content.
EOF

    vc checkout master

    vc merge hotfix

    git branch -q -d hotfix	# NOTE: GIT DEPENDENCY!

    vc checkout iss53

    #C5
    vc commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
Second line of sample2 content.
EOF

    vc checkout master

    vc merge iss53 -m "Second merge."

    #gitk --all
} >"${msgsink}" 2>&1
vc export "A repository with a merge bubble" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

#end
