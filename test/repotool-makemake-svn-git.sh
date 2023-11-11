#!/bin/sh
## Test repotool makemake, svn->git

mkdir /tmp/test-workdir$$
(cd /tmp/test-workdir$$ >/dev/null || ( echo "$0: cd failed" >&2; exit 1 ); ${REPOTOOL:-repotool} makemake xyzzy svn git >/tmp/out$$; echo Return code: $? >>/tmp/out$$)
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

