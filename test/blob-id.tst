## Test use of legacy IDs after a blob command
read :100 <blob-id.svn
blob :101 </dev/null
blob :102 </dev/null
<2> append "appended to legacy rev 2 comment\n"
prefer git
write -
