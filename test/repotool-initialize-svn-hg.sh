#!/bin/sh
## Test repotool initialize, svn->hg

mkdir /tmp/test-workdir$$
cd /tmp/test-workdir$$ >/dev/null || ( echo "$0: cd failed" >&2; exit 1 )
${REPOTOOL:-repotool} initialize xyzzy svn hg >/tmp/out$$
echo Return code: $? >>/tmp/out$$
cd - >/dev/null || ( echo "$0: cd failed" >&2; exit 1 )
./dir-md5 /tmp/test-workdir$$ >>/tmp/out$$

# shellcheck disable=SC1091
. ./common-setup.sh
toolmeta "$1" /tmp/out$$

st=$?
if [ $st -eq 0 ]; then
	rm -rf /tmp/test-workdir$$ /tmp/out$$
fi

exit $st

#end

