#! /bin/sh
## Test repocutter propdel
# Output should reveal deletion of the propset line
trap 'rm -f /tmp/propdel-before$$' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <vanilla.svn >/tmp/propdel-before$$
${REPOCUTTER:-repocutter} -q propdel foo <vanilla.svn | repocutter -q see >/tmp/propdel-after$$
diff --label Before --label After -u /tmp/propdel-before$$ /tmp/propdel-after$$
exit 0

