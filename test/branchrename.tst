## Test the rename branch command
read <deleteall.fi
branch samplebranch rename jabberwocky
branch /(.*)branch2/ rename fuddle\1
write -
