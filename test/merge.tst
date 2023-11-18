## Testing merge and unmerge commands
set flag echo
read <sample1.fi
:31 list inspect
:31 unmerge
:31 list inspect
:29 list inspect 
:25,:29 merge 
:29 list inspect
