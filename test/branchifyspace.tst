## Test branchify option with spaces in dir names

# suppress 'illegal branch/tag name "non branch 2" mapped to "nonbranch2"' warning
log -warn

read --branchify=nonbranch1:non\sbranch\s2 <branchifyspace.svn
prefer git
write -
