#!/bin/sh
## Test repocutter swapcheck
# FIXME: repocutter swapcheck test is incomplete
# Needs tests for (1) toplevwl copy with stabdard layout
# undeneath, (2) stanadard layout buried more than one level deep.
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapcheck 2>&1 <<EOF
SVN-fs-dump-format-version: 2

UUID: b56a19b6-d7cb-40c1-bb7e-a4e9d1112045

Revision-number: 0
Prop-content-length: 229
Content-length: 229

K 8
svn:date
V 27
2007-07-29T14:59:43.328125Z
K 17
svn:sync-from-url
V 38
svn://scm.crossflight.co.uk/subversion
K 18
svn:sync-from-uuid
V 36
2f23deb4-8cc3-4544-bbbf-8de6f3a42640
K 24
svn:sync-last-merged-rev
V 5
50096
PROPS-END

Revision-number: 1
Prop-content-length: 113
Content-length: 113

K 10
svn:author
V 3
Ben
K 8
svn:date
V 27
2007-07-29T15:01:46.156250Z
K 7
svn:log
V 15
Initial import.
PROPS-END

Node-path: crossflight
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Revision-number: 2
Prop-content-length: 113
Content-length: 113

K 10
svn:author
V 3
Ben
K 8
svn:date
V 27
2007-07-29T15:06:32.718750Z
K 7
svn:log
V 15
Initial import.
PROPS-END

Node-path: crossflight/src
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Revision-number: 3
Prop-content-length: 97
Content-length: 97

K 10
svn:author
V 3
Ben
K 8
svn:date
V 27
2007-07-29T15:08:42.296875Z
K 7
svn:log
V 0

PROPS-END

EOF


