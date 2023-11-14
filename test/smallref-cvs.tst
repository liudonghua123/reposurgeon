## CVS reference-cookie substitution
read <<EOF
#reposurgeon sourcetype cvs
blob
mark :1
data 29
Content of file1 version 1.1

commit refs/heads/master
mark :2
committer user1 <user1> 631188000 +0000
data 20
Add file1 and file2
M 100644 :1 file1

property cvs-revisions 10 file1 1.1

blob
mark :3
data 29
Content of file1 version 1.2

commit refs/heads/master
mark :4
committer user1 <user1> 631191600 +0000
data 44
Modify file1 (references [[CVS:file1:1.1]])
from :2
M 100644 :3 file1
property cvs-revisions 10 file1 1.2

reset refs/heads/master
from :4

done
EOF
references
prefer git
write -
