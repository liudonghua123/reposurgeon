## Test branchmap option
read --branchify=ProjA/trunk:ProjB/trunk --branchmap=@^([^/]+)/(.*)/$@heads/\1_\2@ <branchmap.svn
prefer git
write -

