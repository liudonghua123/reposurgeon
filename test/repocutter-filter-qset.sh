#! /bin/sh
${REPOSURGEON:-reposurgeon} 'read --no-automatic-ignores -' 'print < BEGIN >' '4 inspect' '/^link /B filter regex /^link //' 'print < AFTER FILTER >' '4 inspect' 'print < Q >' '=Q index' 'print < END >' <<EOF
SVN-fs-dump-format-version: 2

UUID: afe9ae3b-a21b-4e50-a6b7-6238bb7f75f0

Revision-number: 0
Prop-content-length: 56
Content-length: 56

K 8
svn:date
V 27
2022-02-12T23:42:31.920175Z
PROPS-END

Revision-number: 1
Prop-content-length: 115
Content-length: 115

K 10
svn:author
V 8
rekjanov
K 8
svn:date
V 27
2022-02-12T23:43:43.030132Z
K 7
svn:log
V 12
first commit
PROPS-END

Node-path: trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/symlink1
Node-kind: file
Node-action: add
Text-content-md5: f58573f12f1b988e12f7b95fd1f77a45
Text-content-sha1: 1683bdbf0d9ed354037633f59691a09eb7c187ea
Prop-content-length: 33
Text-content-length: 12
Content-length: 45

K 11
svn:special
V 1
*
PROPS-END
link target1

Revision-number: 2
Prop-content-length: 122
Content-length: 122

K 10
svn:author
V 8
rekjanov
K 8
svn:date
V 27
2022-02-12T23:44:44.670306Z
K 7
svn:log
V 19
renamed and changed
PROPS-END

Node-path: trunk/symlink2
Node-kind: file
Node-action: add
Node-copyfrom-rev: 1
Node-copyfrom-path: trunk/symlink1
Text-copy-source-md5: f58573f12f1b988e12f7b95fd1f77a45
Text-copy-source-sha1: 1683bdbf0d9ed354037633f59691a09eb7c187ea
Text-content-md5: 80fc191f4717d4ffcb461c675bb174de
Text-content-sha1: 28755b801d0e4f25224848884f61218ed8b012cb
Text-content-length: 12
Content-length: 12

link target2

Node-path: trunk/symlink1
Node-action: delete


EOF
