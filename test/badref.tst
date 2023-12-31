## Bad from-rev in file with discontiguous revisions
set flag relax
log +warn
read <<EOF
SVN-fs-dump-format-version: 2

UUID: ce8ba131-4c05-4d3a-a8b6-67d702881f40

Revision-number: 0
Prop-content-length: 56
Content-length: 56

K 8
svn:date
V 27
2011-11-30T16:56:49.728021Z
PROPS-END

Revision-number: 1
Prop-content-length: 128
Content-length: 128

K 7
svn:log
V 30
Linear history with tip tags.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:00:55.652068Z
PROPS-END

Node-path: branches
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: tags
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Node-path: trunk
Node-kind: dir
Node-action: add
Prop-content-length: 10
Content-length: 10

PROPS-END


Revision-number: 2
Prop-content-length: 131
Content-length: 131

K 7
svn:log
V 33
We're not exactly onomatopoetic.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:02:46.158886Z
PROPS-END

Node-path: trunk/README
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 46
Text-content-md5: ce90a5f32052ebbcd3b20b315556e154
Text-content-sha1: bae5ed658ab3546aee12f23f36392f35dba1ebdd
Content-length: 56

PROPS-END
The quick brown fox jumped over the lazy dog.


Revision-number: 4
Prop-content-length: 139
Content-length: 139

K 7
svn:log
V 41
This revision exists to be a tag target.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:03:48.512974Z
PROPS-END

Node-path: trunk/README
Node-kind: file
Node-action: change
Text-content-length: 34
Text-content-md5: ea37afb66c1985877f1691a0389a8702
Text-content-sha1: 856ebbbf0bfe5b63ebe03fd2ca4ddda414cf8e01
Content-length: 34

Fourscore and seven years ago...



Revision-number: 5
Prop-content-length: 122
Content-length: 122

K 7
svn:log
V 24
This is an example tag.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:09:01.334786Z
PROPS-END

Node-path: tags/tag1
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 3
Node-copyfrom-path: trunk


Revision-number: 6
Prop-content-length: 123
Content-length: 123

K 7
svn:log
V 25
Our first file creation.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:13:14.873837Z
PROPS-END

Node-path: trunk/creation-example
Node-kind: file
Node-action: add
Prop-content-length: 10
Text-content-length: 43
Text-content-md5: bddf9e633fa1edd01086a566ee523838
Text-content-sha1: 11cbfa6ebb6b8f637dec0921522209fb65dc9eb3
Content-length: 53

PROPS-END
This file exists to be a creation example.


Revision-number: 7
Prop-content-length: 129
Content-length: 129

K 7
svn:log
V 31
A second content modification.

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:14:36.278967Z
PROPS-END

Node-path: trunk/README
Node-kind: file
Node-action: change
Text-content-length: 68
Text-content-md5: 7c03f96b36d37c6f244e61c432f4bcbb
Text-content-sha1: 8fa357cf1d1c90c7b4e304dca70059a68f7bfaa2
Content-length: 68

Fourscore and seven years ago...

And another content modification.


Revision-number: 8
Prop-content-length: 118
Content-length: 118

K 7
svn:log
V 20
Create a second tag

K 10
svn:author
V 3
esr
K 8
svn:date
V 27
2011-11-30T17:15:46.907548Z
PROPS-END

Node-path: tags/tag2
Node-kind: dir
Node-action: add
Node-copyfrom-rev: 7
Node-copyfrom-path: trunk


EOF
print "Avoid having a last command that fails."
