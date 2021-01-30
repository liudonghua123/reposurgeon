## Test dissection of multiproject repo
set testmode
read <multigen.svn \
    --branchify=project1/trunk:project1/branches/*:project1/tags:*
branch rename heads/project1/trunk heads/master
branch rename :heads/project1/branches/(.*): heads/\1
branch delete heads/project2
prefer git
write -
