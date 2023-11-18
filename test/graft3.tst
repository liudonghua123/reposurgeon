## Test graft with materialization
set flag materialize
read <utf8.fi
rename repo grafted-utf8
read <min.fi
:4 graft grafted-utf8
write -
