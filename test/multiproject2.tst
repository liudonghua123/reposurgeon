## Test correct ordering of mixed-branch splits
branchify App_C/trunk App_C/branches/* App_C/tags/* *
branchmap :App_C/trunk:heads/trunk: :App_C/branches:heads: :App_C/tags:tags:
read <<EOF
SVN-fs-dump-format-version: 2

UUID: a1fc4a5c-d7b3-44ed-8848-8ca818df3276

Revision-number: 0
Prop-content-length: 56
Content-length: 56

K 8
svn:date
V 27
2020-08-26T14:56:38.602987Z
PROPS-END

Revision-number: 1
Prop-content-length: 127
Content-length: 127

K 10
svn:author
V 3
jlu
K 8
svn:date
V 27
2020-08-26T16:11:55.430513Z
K 7
svn:log
V 29
Add App_A, App_B, and App_C.

PROPS-END

Node-path: App_A
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_A/trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_A/trunk/foo_A.txt
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 52
Content-length: 62

PROPS-END
Revision is 1, file path is App_A/trunk/foo_A.txt.


Node-path: App_B
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_B/trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_B/trunk/foo_B.txt
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 52
Content-length: 62

PROPS-END
Revision is 1, file path is App_B/trunk/foo_B.txt.


Node-path: App_C
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_C/trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: App_C/trunk/foo_C.txt
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 52
Content-length: 62

PROPS-END
Revision is 1, file path is App_C/trunk/foo_C.txt.



branch App_A delete
branch App_B delete
EOF
write -
