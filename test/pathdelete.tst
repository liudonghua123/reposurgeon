## Test delete path operation
set flag interactive
set flag echo
set flag quiet
read <expunge.svn
1..$ delete path :^releases/v1.0/.*:
write
