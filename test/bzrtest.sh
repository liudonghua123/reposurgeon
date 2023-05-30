#! /bin/sh
# Create basic revision and tagging test for branch-oriented VCS

engine="${1:-bzr}"

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

trap 'rm -fr /tmp/testbranch$$' EXIT HUP INT QUIT TERM

repository init "${engine}" /tmp/testbranch$$

#C1
repository commit sample "First commit" <<EOF
First line of sample content.
EOF

#C1
repository commit sample "Second commit" <<EOF
First line of sample content.
Second line of sample content.
EOF

#C2
repository commit sample "Third commit" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

repository tag foobar

repository export "${engine} test repository"

#end
