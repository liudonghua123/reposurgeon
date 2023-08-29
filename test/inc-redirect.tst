## Test incorporate command with input redirect
set faketime
read <min.fi
@min(=C) incorporate <<EOF
sample.tar
sample2.tar
EOF
write -
