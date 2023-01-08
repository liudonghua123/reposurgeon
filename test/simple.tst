## Test of index, tip, tags, history, paths, expunge and checkout
set echo
read <simple.fi
~=B index
tags
path list
1..$ expunge theory.txt
path list
116 checkout foobar
!ls foobar
!rm -fr foobar
101,103 diff
101,103 manifest 
116 manifest 
116 manifest /^reposurgeon/
:2 setfield comment "The quick brown fox jumped over the lazy dog.\n"
:2 setperm 100755 rs
:2 setfield author "J. Fred Muggs <muggs@foobar.com>"
# Stream enough parts to verify the setfield and setperm operations
:2 inspect
