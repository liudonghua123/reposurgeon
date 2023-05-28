#! /bin/sh
# Create a small test repository with a merge bubble

# shellcheck disable=SC1091
. ./common-setup.sh

need git

set -e

trap 'rm -fr /tmp/bubble$$' EXIT HUP INT QUIT TERM

# Generic machinery

gitgen() {
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
	    # Git seems to reject timestamps with a leading zero
	    export GIT_COMMITTER_DATE="1${ft} +0000" 
	    export GIT_AUTHOR_DATE="1${ft} +0000" 
	    git commit -q -a -m "$text" --author "Fred J. Foonly <fered@foonly.org>"
	    ;;
	checkout)
	    # Checkput branch, crearing if necessary
	    branch="$1"
	    # shellcheck disable=SC2086
	    if [ "$(git branch | grep ${branch})" = "" ]
	    then
		git branch "${branch}"
	    fi
	    git checkout -q "$1";;
	merge)
	    # Perform merge with controlled clock tick.
	    ts=$((ts + 60))
	    ft=$(printf "%09d" ${ts})
	    export GIT_COMMITTER_DATE="1${ft} +0000" 
	    export GIT_AUTHOR_DATE="1${ft} +0000"
	    git merge -q "$@"
	    ;;
	export)
	    # Dump export stream
	    git fast-export --all;;
    esac
}

# The test

# Based on the merge example at
# https://git-scm.com/book/en/v2/Git-Branching-Basic-Branching-and-Merging

gitgen init /tmp/bubble$$

#C1
gitgen commit sample "First commit (master)" <<EOF
First line of sample content.
EOF

#C1
gitgen commit sample "Second commit (master)" <<EOF
First line of sample content.
Second line of sample content.
EOF

#C2
gitgen commit sample "Third commit (master)" <<EOF
First line of sample content.
Second line of sample content.
Third line of sample content.
EOF

gitgen checkout iss53

#C3
gitgen commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
EOF

gitgen checkout master

gitgen checkout hotfix

#C4
gitgen commit sample3 "Fix broken email address" <<EOF
First line of sample3 content.
EOF

gitgen checkout master

gitgen merge hotfix

git branch -q -d hotfix

gitgen checkout iss53

#C5
gitgen commit sample2 "Create new footer [issue 53]" <<EOF
First line of sample2 content.
Second line of sample2 content.
EOF

gitgen checkout master

gitgen merge iss53 -m "Second merge."

#gitk --all

gitgen export

#end
