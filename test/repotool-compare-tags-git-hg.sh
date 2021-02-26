#!/bin/sh
## Test comparing tags between git and hg repo
# Reproduces https://gitlab.com/esr/reposurgeon/issues/39

trap 'rm -rf /tmp/test-repo$$-git /tmp/test-repo$$-hg /tmp/out$$' EXIT HUP INT QUIT TERM

# Should be independent of what strem file we speciy here.
stem=lighttag

# No user-serviceable parts below this line

command -v git >/dev/null 2>&1 || { echo "    Skipped, git missing."; exit 0; }
command -v hg >/dev/null 2>&1 || { echo "    Skipped, hg missing."; exit 0; }

./fi-to-fi -n /tmp/test-repo$$-git <${stem}.fi
./hg-to-fi -n /tmp/test-repo$$-hg <${stem}.fi
${REPOTOOL:-repotool} compare-tags /tmp/test-repo$$-git /tmp/test-repo$$-hg 2>&1 | sed -e "s/$$/\$\$/"g >/tmp/out$$ 2>&1

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$
	      
# end
