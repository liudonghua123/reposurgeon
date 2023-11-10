## Test simplest possible case of copy expunge
read <copy.fi
print "Check that manifest includes sample and sample2"
@max(=C) manifest
expunge "sample2"
print "Last commit should have no fileops"
write -
print "Following command should list sample"
@max(=C) manifest
