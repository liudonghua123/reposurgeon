#!/bin/bash
## Test listing closed branches in hg repository

# shellcheck disable=SC1091
. ./common-setup.sh

need hg

trap 'rm -rf /tmp/test-hg-closed-branches-repo$$ /tmp/out$$' EXIT HUP INT QUIT TERM

create_repo() {
    local repo=$1
    hg init "$repo"
    
    user='"J. Random Hacker" <jrh@foobar.com>'

    touch "$repo/afile"
    hg -R "$repo" add "$repo/afile"
    hg -R "$repo" commit --user "$user" --date "1456976347 18000" -m 'Root Commit'
    hg -R "$repo" -q branch closed-branch
    echo something > "$repo/afile"
    hg -R "$repo" commit --user "$user" --date "1456976348 18000" -m 'Add Branch'
    hg -R "$repo" commit --user "$user" --date "1456976349 18000" --close-branch -m 'Close Branch'
}

repo=/tmp/test-hg-closed-branches-repo$$
create_repo "$repo"
(tapcd "$repo"; ${REPOTOOL:-repotool} branches) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

toolmeta "$1" /tmp/out$$

# end

