#!/bin/sh
## Test node deselection
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 3339.1,4431.11:4431:16 deselect 2>&1 <<EOF
SVN-fs-dump-format-version: 2

UUID: c97812fc-d253-487b-8882-a03e205d4398

Revision-number: 0
Prop-content-length: 242
Content-length: 242

K 8
svn:date
V 27
2007-03-30T17:06:04.154851Z
K 17
svn:sync-from-url
V 51
https://pl3.projectlocker.com/LiveData/livedata/svn
K 18
svn:sync-from-uuid
V 36
940ffce2-e72c-0410-a864-ee9130fecfe5
K 24
svn:sync-last-merged-rev
V 5
39583
PROPS-END

Revision-number: 3339
Prop-content-length: 141
Content-length: 141

K 10
svn:author
V 8
Cditrani
K 8
svn:date
V 27
2009-06-10T15:04:42.258878Z
K 7
svn:log
V 38
Added HFM tree, correctly this time.


PROPS-END

Node-path: HFM
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/Apache2
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/Apache2/conf
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/Apache2/conf/certs
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/LiveData
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/LiveData/Server
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/LiveData/Server/Config
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/version
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 50
Content-length: 60

PROPS-END
Revision is 3339, file path is HFM/trunk/version.


Revision-number: 3951
Prop-content-length: 175
Content-length: 175

K 10
svn:author
V 8
Cditrani
K 8
svn:date
V 27
2009-10-15T20:29:28.317663Z
K 7
svn:log
V 72
Added apache certs and updated install to include current set of files.

PROPS-END

Node-path: trunk/HFM/Apache2/conf/certs/livedata_hfhs_org.crt
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 83
Content-length: 93

PROPS-END
Revision is 3951, file path is HFM/trunk/Apache2/conf/certs/livedata_hfhs_org.crt.


Revision-number: 3952
Prop-content-length: 128
Content-length: 128

K 10
svn:author
V 8
Cditrani
K 8
svn:date
V 27
2009-10-15T20:43:10.976345Z
K 7
svn:log
V 25
Branched for release 1.0

PROPS-END

Node-path: branches/HFM1.0
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 3951
Node-copyfrom-path: trunk/HFM


Revision-number: 4431
Prop-content-length: 149
Content-length: 149

K 10
svn:author
V 8
Cditrani
K 8
svn:date
V 27
2010-02-03T17:48:40.881312Z
K 7
svn:log
V 46
Merge -r3952:4430 from HFM1.0/HFM1.0.2 branch

PROPS-END

Node-path: trunk/HFM
Node-kind: dir
Node-action: change
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk/HFM/src
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src


Node-path: trunk/HFM/src/Apache2
Node-action: delete

Node-path: trunk/HFM/src/Apache2
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/Apache2


Node-path: trunk/HFM/src/Apache2/conf
Node-action: delete

Node-path: trunk/HFM/src/Apache2/conf
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/Apache2/conf


Node-path: trunk/HFM/src/Apache2/conf/certs
Node-action: delete

Node-path: trunk/HFM/src/Apache2/conf/certs
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/Apache2/conf/certs


Node-path: trunk/HFM/src/Apache2/conf/certs/livedata_hfhs_org.crt
Node-action: delete

Node-path: trunk/HFM/src/Apache2/conf/certs/livedata_hfhs_org.crt
Node-kind: file
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/Apache2/conf/certs/livedata_hfhs_org.crt


Node-path: trunk/HFM/src/LiveData
Node-action: delete

Node-path: trunk/HFM/src/LiveData
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/LiveData


Node-path: trunk/HFM/src/LiveData/Server
Node-action: delete

Node-path: trunk/HFM/src/LiveData/Server
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/LiveData/Server


Node-path: trunk/HFM/src/LiveData/Server/Config
Node-action: delete

Node-path: trunk/HFM/src/LiveData/Server/Config
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 4429
Node-copyfrom-path: branches/HFM1.0/HFM/src/LiveData/Server/Config


Node-path: trunk/HFM/version
Node-kind: file
Node-action: change
Text-content-length: 50
Content-length: 50

Revision is 4431, file path is HFM/trunk/version.


Node-path: trunk/HFM/Apache2
Node-action: delete


Node-path: trunk/HFM/LiveData
Node-action: delete


EOF
