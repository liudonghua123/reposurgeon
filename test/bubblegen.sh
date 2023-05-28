#! /bin/sh
# Create a small test repository with a merge bubbke
# FIXME: We don't have the right sequence of operationv in bubblegen.sh

# shellcheck disable=SC1091
. ./common-setup.sh

need git

set -e

trap 'rm -fr /tmp/bubble$$' EXIT HUP INT QUIT TERM

# Generic machinery

pseudogit() {
    cmd="$1"
    shift
    case "${cmd}" in
	init)
	    # Initialize repo in specified temporary directory
	    base="$1";
	    rm -fr "${base}";
	    mkdir "${base}";
	    cd "${base}" >/dev/null;
	    git init -q;
	    ts=0;
	    ;;
	commit)
	    # Add or commit content
	    file="$1"
	    text="$2"
	    cat >"${file}"
	    if [ -f "${file}" ]
	    then
		git add "${file}"
	    fi
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    # Git seems to reject timestamps with a leading xero
	    export GIT_COMMITTER_DATE="1${ft} +0000" 
	    export GIT_AUTHOR_DATE="1${ft} +0000" 
	    git commit -q -a -m "$text" --author "Fred J. Foonly <fered@foonly.org>"
	    ;;
	checkout)
	    # Checkput branch, crearing if necessary
	    branch="$1"
	    if [ "$(git branch | grep ${branch})" = "" ]
	    then
		git branch "${branch}"
	    fi
	    git checkout -q "$1";;
	export)
	    # Dump export stream
	    git fast-export --all;;
    esac
}

# The test

pseudogit init /tmp/bubble$$

pseudogit commit sample "First commit (master)" <<EOF
First line of sample content.
EOF

pseudogit commit sample "Second commit (master)" <<EOF
First line of sample content.
Second line of sample content.
EOF

pseudogit checkout foobar

pseudogit commit sample2 "Third commit (foobar)" <<EOF
First line of sample2 content.
EOF

pseudogit checkout master

pseudogit commit sample "Fourth commit (master)" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

pseudogit merge foobar

pseudogit commit sample2 "Fifth commit" <<EOF
First line of sample2 content.
Second line of sample2 content.
EOF

pseudogit export

#end
