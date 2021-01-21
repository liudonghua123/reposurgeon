## Test subversion tags nested below the root
read <nesting.svn \
     --branchify=cpp-msbuild/trunk:cpp-msbuild/branches/*:cpp-msbuild/tags/*
branch heads/cpp-msbuild/trunk rename heads/master
branch @cpp-msbuild/tags/(.*)@ rename \1
prefer git
write -
