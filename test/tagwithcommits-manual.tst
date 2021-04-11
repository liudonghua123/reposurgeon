## Test commit after tag creation, but with manual branchlifting.

read <tagwithcommits.svn --nobranch

path rename "trunk/(.*)" "\1"

# Tagify commit <3>
<3> expunge @^tags/@
tag rename emptycommit-3 1.0

# Tagify commit <4>
<4> expunge @^tags/@
tag rename emptycommit-4 2.0-root

# branchlift remaining commits in tags/2.0
branchlift master tags/2.0 2.0
branch rename refs/heads/2.0 tags/2.0

# renumbering is required for identical result as "auto-branchified" tagwithcommits.tst
renumber

prefer git
write -
