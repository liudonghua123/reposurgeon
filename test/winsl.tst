## Test Windows10/WSL line termination in message boxes
# This was Florian Eßer's tag create case that core dumped.
# There should be a CR on the header delimiter line. 
read <min.fi
@max(=C) create tag cutover-git
msgin <<EOF
Tag-Name: cutover-git
Tagger: J. Random Hacker <jrh@random.org> America/Los_Angeles

This tag marks the last Subversion commit before the move to Git.
EOF
write -
