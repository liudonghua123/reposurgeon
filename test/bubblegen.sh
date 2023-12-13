#! /bin/sh
# Create a small test repository with a merge bubble
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

trap 'rm -fr /tmp/bubble$$' EXIT HUP INT QUIT TERM

# Based on the merge example at
# https://git-scm.com/book/en/v2/Git-Branching-Basic-Branching-and-Merging

repository init git

#C1
repository commit sample "First commit (master)" <<EOF
First line of sample content.
EOF

#C1
repository commit sample "Second commit (master)" <<EOF
First line of sample content.
Second line of sample content.
EOF

#C2
repository commit sample "Third commit (master)" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

repository checkout iss53

#C3
repository commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
EOF

repository checkout master

repository checkout hotfix

#C4
repository commit sample3 "Fix broken email address" <<EOF
First line of sample3 content.
EOF

repository checkout master

repository merge hotfix

git branch -q -d hotfix	# NOTE: GIT DEPENDENCY!

repository checkout iss53

#C5
repository commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
Second line of sample2 content.
EOF

repository checkout master

repository merge iss53 -m "Second merge."

#gitk --all

repository export "A repository with a merge bubble"

#end
