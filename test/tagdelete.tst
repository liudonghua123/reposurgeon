## Test deletion of annotated tags
read <sample4.fi
1..$ delete tag annotated
print "Check file should not contain '33	tag	annotated'"
tags
