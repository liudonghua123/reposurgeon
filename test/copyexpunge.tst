## Test simplest possible case of copy delete path
read <copy.fi
print "Check that manifest includes sample and sample2"
@max(=C) manifest
delete path "sample2"
print "Last commit should have no fileops"
write -
print "Following command should list sample"
@max(=C) manifest
