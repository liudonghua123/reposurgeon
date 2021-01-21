## Test branchmap option
read --branchify=ProjA/trunk:ProjB/trunk <branchmap.svn
branch @heads/([^/]+)/(.*)@ rename heads/\1_\2 
prefer git
write -

