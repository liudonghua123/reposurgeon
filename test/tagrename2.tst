## Trickier tag rensme test
read --branchify=trunk:branches/*:tags/*:vendor/* <vendorbranch.svn
print Before tag-branch rename
names
<2>,<4> merge  ## add missing merge
branch vendor/5.4 delete ## remove branch of tag
tag "vendor/5.4-root" rename "vendor/5.4"
print After tag-branch rename
names
