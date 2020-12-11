## Test branch-lifting primitive
read --nobranch <deepdirs.svn
print Before
:10 inspect
branchlift master branches/foocopy hotcopy
print After
:10 inspect
