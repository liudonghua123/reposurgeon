## Test path rename capability
set flag relax
read <sample1.fi
rename path «README« REAMDE	# Should succeed (deliberate use of unicode punctuation to test the parser at the same time)
rename path /.gitignore/ REAMDE	# Should fail
write -
