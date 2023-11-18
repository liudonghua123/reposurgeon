## Test unite of multiple copies of a repo mapped to branches
read <bzr.fi
rename path /^(.*)/ foo/${1}
rename repo foo
read <bzr.fi
rename path /^(.*)/ bar/${1}
rename repo bar
unite foo bar
write -
