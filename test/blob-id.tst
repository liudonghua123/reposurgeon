## Test use of legacy IDs after a blob command
read <blob-id.svn
create blob :101 </dev/null
create blob :102 </dev/null
<2> append "appended to legacy rev 2 comment\n"
prefer git
write -
