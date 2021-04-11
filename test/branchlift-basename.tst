## Test branch-lifting primitive
read --nobranch <deepdirs.svn
print Before
:10 inspect
# should create a branch named foocopy
branchlift master branches/foocopy
print After
:10 inspect
