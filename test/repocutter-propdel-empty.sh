#! /bin/sh
## Test repocutter propdel on durectory
trap 'rm -f /tmp/propdel-before$$ /tmp/propdel-after$$' EXIT HUP INT QUIT TERM
cat >/tmp/propdel-before$$ <<EOF
Revision-number: 7952
Prop-content-length: 147
Content-length: 147

K 10
svn:author
V 21
cditrani@livedata.com
K 8
svn:date
V 27
2012-08-21T11:38:48.212635Z
K 7
svn:log
V 30
adding folder for nightly qa.

PROPS-END

Node-path: Infrastructure/branches/nqa
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Revision-number: 7953
Prop-content-length: 181
Content-length: 181

K 10
svn:author
V 21
cditrani@livedata.com
K 8
svn:date
V 27
2012-08-21T12:24:57.196822Z
K 7
svn:log
V 64
Delete dup version of ezmail.py and replace with external link.

PROPS-END

Node-path: qa/branches
Node-kind: dir
Node-action: change
Prop-content-length: 92
Content-length: 92

K 13
svn:externals
V 57
^/CoreServer/trunk/Source/Python/lib/ezmail.py ezmail.py

PROPS-END

Revision-number: 7954
Prop-content-length: 190
Content-length: 190

K 10
svn:author
V 21
cditrani@livedata.com
K 8
svn:date
V 27
2012-08-21T13:20:43.370539Z
K 7
svn:log
V 73
Stop sending .zip files as .foo (not necessary). Attach qa.log directly.

PROPS-END

Node-path: qa/branches/qa.py
Node-kind: file
Node-action: change
Text-content-length: 41
Content-length: 41

Revision is 7954, file path is qa/qa.py.


EOF
${REPOCUTTER:-repocutter} -q propdel svn:externals </tmp/propdel-before$$ >/tmp/propdel-after$$
diff --label Before --label After -u /tmp/propdel-before$$ /tmp/propdel-after$$
exit 0

