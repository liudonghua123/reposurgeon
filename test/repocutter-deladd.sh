#! /bin/sh
## Test repocutter handling of delete followed by add
# Output should retain both delerte and add

# shellcheck disable=SC1091
. ./common-setup.sh
seecompare swapsvn <<EOF
Revision-number: 13316
Prop-content-length: 117
Content-length: 117

K 10
svn:author
V 22
esalinger@livedata.com
K 8
svn:date
V 27
2015-03-17T14:57:02.438015Z
K 7
svn:log
V 0

PROPS-END

Node-path: SchedulePlanner
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END
Revision-number: 13317
Prop-content-length: 117
Content-length: 117

K 10
svn:author
V 22
esalinger@livedata.com
K 8
svn:date
V 27
2015-03-17T14:57:47.804946Z
K 7
svn:log
V 0

PROPS-END

Node-path: SchedulePlanner/trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END
Revision-number: 14191
Prop-content-length: 181
Content-length: 181

K 10
svn:author
V 21
cditrani@livedata.com
K 8
svn:date
V 27
2015-08-10T20:24:09.641511Z
K 7
svn:log
V 64
Copy current trunk to branch and delete. We'll recreate next...

PROPS-END

Node-path: SchedulePlanner/branches/Aug-10-Save
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 14190
Node-copyfrom-path: SchedulePlanner/trunk

Node-path: SchedulePlanner/trunk
Node-action: delete

Revision-number: 14192
Prop-content-length: 136
Content-length: 136

K 10
svn:author
V 21
cditrani@livedata.com
K 8
svn:date
V 27
2015-08-10T20:24:37.759422Z
K 7
svn:log
V 19
Adding back trunk.

PROPS-END

Node-path: SchedulePlanner/trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END
EOF

