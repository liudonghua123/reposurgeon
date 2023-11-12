## Test handling of progress directive
read <<EOF
progress (reading repository)
progress 1: fubble
commit refs/heads/master
mark :1
committer esr <esr@thyrsus.com> 1699796398 +0000
data 6
fubble
M 100644 inline bar
data 4
bar

progress (patches converted)
progress (cleaning up)
progress done
EOF
write -
