## Test import command with input redirect
set flag fakeuser
read <min.fi
@min(=C) import <<EOF
sample.tar
sample2.tar
EOF
write -
