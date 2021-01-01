## Test dissection of multiproject repo
set testmode
read <multigen.svn \
    --branchify=project1/trunk:project1/branches/*:project1/tags:* \
    --branchmap=@project1/trunk@heads/master@ \
    --branchmap=@project1/tags@tags@ \
    --branchmap=@project1/branches@branches@
branch project2 delete
prefer git
write -
