## Test rename to empty path
read <branchreplace.svn
list paths
# try to move all files fron data/ to root
rename path @data/@ ""
list paths
