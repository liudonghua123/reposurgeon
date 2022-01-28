#! /bin/sh
## Test repocutter setpath
# Output should reveal alteration of the node path
trap 'rm -f /tmp/setpath-before$$' EXIT HUP INT QUIT TERM
${REPOCUTTER:-repocutter} -q see <simpletag.svn >/tmp/setpath-before$$
${REPOCUTTER:-repocutter} -q -r 7.1 setpath arglebargle <simpletag.svn | repocutter -q see >/tmp/setpath-after$$
diff --label Before --label After -u /tmp/setpath-before$$ /tmp/setpath-after$$
exit 0

