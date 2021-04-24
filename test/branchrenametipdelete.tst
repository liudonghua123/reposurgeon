## Test that the rename branch command also renames associated tags
read --preserve <branchrenametipdelete.svn

branch rename /first-branch/ deleted-branch

write -
