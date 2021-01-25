## Trickier tag rename test
read --branchify=trunk:branches/*:tags/*:vendor/* <vendorbranch.svn
print Before tag-branch rename
names
<2>,<4> merge
branch vendor/5.4 delete
tag "vendor/5.4-root" rename "vendor/5.4"
print After tag-branch rename
names
