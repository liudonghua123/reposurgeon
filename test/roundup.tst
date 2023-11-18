## Test squash --pushback
set flag echo
read <roundup.fi
set flag interactive
:1? resolve
:39,:42 list inspect
:42 squash --pushback
:39 list inspect
