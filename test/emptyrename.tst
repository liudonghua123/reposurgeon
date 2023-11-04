## Test rename to empty path
read <branchreplace.svn
path list
# try to move all files fron data/ to root
path rename @data/@ ""
path list
