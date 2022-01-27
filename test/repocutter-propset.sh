#! /bin/sh
## Test repocutter propset
# Output should reveal alteration of the propset line
trap 'rm -f /tmp/propset-before$$' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <vanilla.svn >/tmp/propset-before$$
${REPOCUTTER:-repocutter} -q -r 5.1 propset foo=qux <vanilla.svn | repocutter -q see >/tmp/propset-after$$
diff --label Before --label After -u /tmp/propset-before$$ /tmp/propset-after$$
exit 0

