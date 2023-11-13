## Test squash --pushback
set flag echo
read <roundup.fi
set flag interactive
:1? resolve
:39,:42 inspect
:42 squash --pushback
:39 inspect
