## Split a commit based on a directory prefix
set echo
set relax
set interactive 
set quiet
read <split-dir.svn
:2 split by bar
# Expect the split on zed to fail
:5 split by zed
:5 split by f
inspect
