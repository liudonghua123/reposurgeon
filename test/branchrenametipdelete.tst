## Test that the rename branch command also renames associated tags
read --preserve <branchrenametipdelete.svn

rename branch /first-branch/ deleted-branch

write -
