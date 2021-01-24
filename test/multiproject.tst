## Test dissection of multiproject repo
set testmode
read <multigen.svn \
    --branchify=project1/trunk:project1/branches/*:project1/tags:*
branch heads/project1/trunk rename heads/master
branch :heads/project1/branches/(.*): rename heads/\1
branch heads/project2 delete
prefer git
write -
