#!/bin/sh
## Test property propagation over directory copies.
# This used to be in the svncheck tests, but it had to be moved
# when we implemented struct header checking on fast-import streams,
# because it didn't produce a psrseable stream.
trap 'rm -fr  /tmp/foo$$ /tmp/bar$$' EXIT HUP INT QUIT TERM 
reposurgeon "log +properties" "set logfile /tmp/foo$$" "read <dircopyprop.svn"
sed </tmp/foo$$ "/^[^Z]*Z:/s///" >/tmp/bar$$	# Strip off the date stamp
cat >/tmp/chk$$ <<EOF
 r3.1~trunk/testdir/foo properties set:
 	someprop = "Test property."
 r4.1~trunk/testdir/foo properties set:
 	someprop = "Test property modified.\n"
 r5.2~trunk/testdir2/foo properties set:
 	someprop = "Test property modified again with directory copy.\n"
EOF
if cmp /tmp/bar$$ /tmp/chk$$
then
    echo "ok - $0: succeeded"; exit 0
else
    echo "not ok - $0: failed"; exit 1
fi

# end
