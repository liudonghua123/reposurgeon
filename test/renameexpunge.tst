## Test simplest possible case of rename delete path
read <rename.fi
print "Check that list manifest includes just sample2"
@max(=C) list manifest
delete path "sample2"
print "Last commit should have no fileops"
write -
print "Following command should list sample"
@max(=C) list manifest

