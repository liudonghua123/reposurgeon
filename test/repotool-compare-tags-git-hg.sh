#!/bin/sh
## Test comparing tags between git and hg repo
# Reproduces https://gitlab.com/esr/reposurgeon/issues/39

# Should be independent of what stem file we specify here.
stem=lighttag

# No user-serviceable parts below this line

# shellcheck disable=SC1091
. ./common-setup.sh

need git hg

trap 'rm -rf /tmp/test-repo$$-git /tmp/test-repo$$-hg /tmp/out$$' EXIT HUP INT QUIT TERM

./fi-to-fi -n /tmp/test-repo$$-git <${stem}.fi
./hg-to-fi -n /tmp/test-repo$$-hg <${stem}.fi
${REPOTOOL:-repotool} compare-tags /tmp/test-repo$$-git /tmp/test-repo$$-hg 2>&1 | sed -e "s/$$/\$\$/"g >/tmp/out$$ 2>&1

toolmeta "$1" /tmp/out$$
	      
# end
