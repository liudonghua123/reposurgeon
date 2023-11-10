## Test simplest possible case of rename expunge
read <rename.fi
print "Check that manifest includes just sample2"
@max(=C) manifest
expunge "sample2"
print "Last commit should have no fileops"
write -
print "Following command should list sample"
@max(=C) manifest

