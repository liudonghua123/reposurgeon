#!/bin/bash
## Test listing closed branches in hg repository

command -v hg >/dev/null 2>&1 || { echo "    Skipped, hg missing."; exit 0; }

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
(cd "$repo" || (echo "$0: cd failed" >&2; exit 1); ${REPOTOOL:-repotool} branches) >/tmp/out$$ 2>&1
echo Return code: $? >>/tmp/out$$

case $1 in
    --regress)
        diff --text -u repotool-hg-closed-branches.chk /tmp/out$$ || ( echo "$0: FAILED"; exit 1 ); ;;
    --rebuild)
	cat /tmp/out$$ >repotool-hg-closed-branches.chk;;
    --view)
	cat /tmp/out$$;;
esac
