#! /bin/sh
## Test repocutter setcopyfrom
# Output should reveal alteration of the copyfrom path
trap 'rm -f /tmp/setcopyfrom-before$$' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <simpletag.svn >/tmp/setcopyfrom-before$$
${REPOCUTTER:-repocutter} -q -r 7.1 setcopyfrom arglebargle <simpletag.svn | repocutter -q see >/tmp/setcopyfrom-after$$
diff --label Before --label After -u /tmp/setcopyfrom-before$$ /tmp/setcopyfrom-after$$
exit 0

