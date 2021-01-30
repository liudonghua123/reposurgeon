## Test the rename branch command
read <deleteall.fi
branch rename heads/samplebranch heads/jabberwocky
branch rename /heads.(.*)branch2/ heads/fuddle\1
write -
