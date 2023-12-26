#! /bin/sh
# Basic revision and tagging test for VCSes with exporters.
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

engine="${1:-bzr}"

here=$(pwd)
{
    vc init "${engine}"

    #C1
    vc commit sample "First commit" <<EOF
First line of sample content.
EOF

    #C1
    vc commit sample "Second commit" <<EOF
First line of sample content.
Second line of sample content.
EOF

    #C2
    vc commit sample "Third commit" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

    vc tag foobar
} >"${msgsink}" 2>&1
vc export "${engine} test repository" >"${outsink}"

# With -o, don't ship to the target until we know we have not errored out
if [ -s /tmp/genout$$ ]
then
    cp /tmp/genout$$ "${here}/${target}"
fi

#end
