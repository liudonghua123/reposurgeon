## Test synthesis of a repository using create and msgin
create repo foo
shell echo "Alpha content" >alpha
shell echo "Beta content" >beta
msgin --create <<EOF
------------------------------------------------------------------------
Committer: Eric S. Raymond <esr@thyrsus.com>
Committer-Date: Thu, 07 Sep 2023 19:05:49 +0000
Branch: refs/heads/master
Content-Path: alpha
Content-Name: fubble

Make an example commit using the synthetic alpha file
------------------------------------------------------------------------
Committer: Eric S. Raymond <esr@thyrsus.com>
Committer-Date: Thu, 07 Sep 2023 19:06:49 +0000
Branch: refs/heads/master
Content-Path: beta

Make an example commit using the synthetic beta file
------------------------------------------------------------------------
EOF
$ create reset refs/heads/master
write -
shell rm alpha beta
# end

