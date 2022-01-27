#! /bin/sh
## Test repocutter proprenaname
# Output should reveal alteration of the proprenaname line
trap 'rm -f /tmp/proprenaname-before$$' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <vanilla.svn >/tmp/proprenaname-before$$
${REPOCUTTER:-repocutter} -q proprename 'foo->wibble' <vanilla.svn | repocutter -q see >/tmp/proprenaname-after$$
diff --label Before --label After -u /tmp/proprenaname-before$$ /tmp/proprenaname-after$$
exit 0

