## Test recovery from malformed search
set flag fakeuser
set flag relax
read <simple.fi
list
print "Expect malformed text specifier message"
/
print "First listing - should not be truncated"
list
print "Second listing - should not be truncated"
=C list
