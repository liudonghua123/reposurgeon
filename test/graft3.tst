## Test graft with materialization
set materialize
read <utf8.fi
rename grafted-utf8
read <min.fi
:4 graft grafted-utf8
write -
