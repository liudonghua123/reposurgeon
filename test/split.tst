## Test the split command
set flag echo
set flag interactive
set flag quiet
set flag relax
read <mergeinfo.svn
:6 split 2
prefer git
inspect
print "Avoid having a last command that fails"
