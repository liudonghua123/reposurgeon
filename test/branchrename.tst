## Test the rename branch command
read <deleteall.fi
branch heads/samplebranch rename heads/jabberwocky
branch /heads.(.*)branch2/ rename heads/fuddle\1
write -
