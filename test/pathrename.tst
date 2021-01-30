## Test path rename capability
set relax
read <sample1.fi
path rename /README/ REAMDE	# Should succeed
path rename /.gitignore/ REAMDE	# Should fail
write -
