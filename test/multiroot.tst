## Test unite of multiple copies of a repo mapped to branches
read <bzr.fi
path rename /^(.*)/ foo/${1}
rename repo foo
read <bzr.fi
path rename /^(.*)/ bar/${1}
rename repo bar
unite foo bar
write -
