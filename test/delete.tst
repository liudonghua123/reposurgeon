## Test if delete command can delete all types of objects

set flag echo
# Use --quiet so that adding commits to the test files doesn't break the test
read <liftlog.fi
1..$ delete --quiet
list inspect

read <testrepo.fi
1..$ delete --quiet
list inspect

