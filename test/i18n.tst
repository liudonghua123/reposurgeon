## Test =J and transcode
read <<EOF
# This stream dump contains one Latin-1 character
blob
mark :1
data 20
1234567890123456789

commit refs/heads/master
mark :2
committer Ralf Schlatterbeck <rsc@runtux.com> 0 +0000
data 14
First commit.
M 100644 :1 README

blob
mark :3
data 20
0123456789012345678

commit refs/heads/master
mark :4
committer Ralf Schlatterbeck <rsc@runtux.com> 10 +0000
data 15
Second commit.
from :2
M 100644 :3 README

blob
mark :398168
data 16
Blob at :398168

blob
mark :398169
data 16
Blob at :398169

reset refs/heads/master^0
commit refs/heads/master
mark :398170
author Johan Bockg�rd <bojohan@gnu.org> 1383515485 +0100
committer Johan Bockg�rd <bojohan@gnu.org> 1383515485 +0100
data 176
Attribution lines contain a Latin-1 character.

* cedet/semantic/lex.el (semantic-lex-start-block)
(semantic-lex-end-block): Move after definition of
semantic-lex-token macro.
M 100644 :398168 lisp/cedet/ChangeLog
M 100644 :398169 lisp/cedet/semantic/lex.el

EOF
set flag interactive
=J resolve
clear flag interactive
=I transcode latin1
write -
