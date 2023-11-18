## Test the rename branch command
read <deleteall.fi
rename branch heads/samplebranch heads/jabberwocky
rename branch /heads.(.*)branch2/ heads/fuddle${1}
write -
