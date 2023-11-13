## Test file expunge operation
set flag interactive
set flag echo
set flag quiet
read <expunge.svn
1..$ expunge /^releases\/v1.0\/.*/
write
