## Test msgin with commt match by msgout --id headers
read <sample1.fi
msgin <<EOF
------------------------------------------------------------------------
Committer-Date: Sun, 02 Dec 2012 00:37:55 -0500
Check-Text: A start on a test repository for the Subversion dumper

THIS LINE SHOULD BE VISBLY MODIFIED.
EOF
write -
