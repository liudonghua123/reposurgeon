## Test merging multiple root commits into single trunk
read <multiprojectmerge.svn

# history of files from former "software" subproject is preserved already because that copy came first.

# try to also preserve history of files from former "firmware" subproject:
<7>,<3> merge

# try to also preserve history of files from former "docs" subproject:
<8>,<4> merge

write -

prefer git
# # FIXME: this generates an invalid input stream that fails to fast-import with:
# #    fatal: Invalid ref name or SHA1 expression: refs/heads/docs^0
# # see https://gitlab.com/esr/reposurgeon/-/issues/356
# rebuild multiprojectmerge-git
