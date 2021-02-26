#!/bin/sh
## Test repotool initialize, cvs->git

mkdir /tmp/test-workdir$$
cd /tmp/test-workdir$$ || ( echo "$0: cd failed"; exit 1 )
${REPOTOOL:-repotool} initialize xyzzy cvs git >/tmp/out$$
echo Return code: $? >>/tmp/out$$
# shellcheck disable=2064
cd - >/dev/null || ( echo "$0: cd failed"; exit 1 )
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

