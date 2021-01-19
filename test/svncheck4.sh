#!/bin/sh
## Test property propagation over directory copies
trap 'rm -fr  /tmp/foo$$' EXIT HUP INT QUIT TERM 
reposurgeon "log +properties" "logfile /tmp/foo$$" "read <dircopyprop.svn"
sed </tmp/foo$$ "/^[^Z]*Z:/s///"	# Strip off the date stamp
