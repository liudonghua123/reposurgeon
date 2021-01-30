## Trickier tag rename test
read --branchify=trunk:branches/*:tags/*:vendor/* <vendorbranch.svn
print Before tag-branch rename
names
<2>,<4> merge
branch delete vendor/5.4
tag rename 'vendor/5.4-root' vendor/5.4
print After tag-branch rename
names
