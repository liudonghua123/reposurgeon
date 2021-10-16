## Test file expunge operation
set interactive
set echo
set quiet
read <expunge.svn
1..$ expunge /^releases\/v1.0\/.*/
write
