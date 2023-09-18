## Test clone primitive
read <bs.fi
clone
choose
write >/tmp/rsclone$$
print Between this line
shell diff bs.fi /tmp/rsclone$$
print and this line, there should be nothing
shell rm /tmp/rsclone$$
