## Split a commit based on a directory prefix
set echo
set relax
set interactive 
set quiet
read <split-dir.svn
:2 split bar
# Expect the split on zed to fail
:5 split zed
:5 split --path f
inspect
