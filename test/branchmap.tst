## Test branchmap option
read --branchify=ProjA/trunk:ProjB/trunk <branchmap.svn

branch rename @heads/([^/]+)/(.*)@ heads/\1_\2 
prefer git
write -

