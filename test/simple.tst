## Test of index, tip, tags, history, paths, delete path and checkout
set flag echo
read <simple.fi
~=B list index
list tags
list paths
1..$ delete path theory.txt
list paths
116 checkout foobar
!ls foobar
!rm -fr foobar
101,103 diff
101,103 list manifest 
116 list manifest 
116 list manifest /^reposurgeon/
:2 setfield comment "The quick brown fox jumped over the lazy dog.\n"
:2 setperm 100755 rs
:2 setfield author "J. Fred Muggs <muggs@foobar.com>"
# Stream enough parts to verify the setfield and setperm operations
:2 list inspect
# Following three line should render to the same RFC3339Z stamp
show when 1287754582 +0400
show when 1287754582 +0000
show when 1287754582
show when 2010-10-22T13:36:22Z
