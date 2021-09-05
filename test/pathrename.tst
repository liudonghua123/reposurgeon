## Test path rename capability
set relax
read <sample1.fi
path rename «README« REAMDE	# Should succeed (deliberate use of unicode punctuation to test the parser at the same time)
path rename /.gitignore/ REAMDE	# Should fail
write -
