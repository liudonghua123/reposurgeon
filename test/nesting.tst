## Test subversion tags nested below the root
read <nesting.svn \
     --branchify=cpp-msbuild/trunk:cpp-msbuild/branches/*:cpp-msbuild/tags/* \
     --branchmap=@cpp-msbuild/trunk/@heads/master@ \
     --branchmap=@cpp-msbuild/tags/(.*)/@tags/\1@
prefer git
write -
