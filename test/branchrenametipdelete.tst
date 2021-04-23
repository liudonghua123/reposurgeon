## Test that the rename branch command also renames associated tags
read --preserve <branchrenametipdelete.svn

# replace spaces in branch names with -
# see https://gitlab.com/esr/reposurgeon/-/issues/358
branch rename /\s+/ -

write -
