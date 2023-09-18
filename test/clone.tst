## Test clone primitive
read <bs.fi
clone
choose
write >/tmp/rsclone$$
print Between this line
shell diff -u bs.fi /tmp/rsclone$$ || exit 0
print and this line, there should be nothing
shell rm /tmp/rsclone$$
