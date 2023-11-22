## Test incorporate command with input redirect
set flag fakeuser
read <min.fi
@min(=C) incorporate <<EOF
sample.tar
sample2.tar
EOF
write -
