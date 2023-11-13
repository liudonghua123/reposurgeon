## Split a commit based on a directory prefix
set flag echo
set flag relax
set flag interactive 
set flag quiet
read <split-dir.svn
:2 split bar
# Expect the split on zed to fail
:5 split zed
:5 split --path f
inspect
