## Test subversion tags nested below the root
read <nesting.svn \
     --branchify=cpp-msbuild/trunk:cpp-msbuild/branches/*:cpp-msbuild/tags/*
branch rename heads/cpp-msbuild/trunk heads/master
branch rename @cpp-msbuild/tags/(.*)@ \1
prefer git
write -
