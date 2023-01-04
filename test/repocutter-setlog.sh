#! /bin/sh
## Test repocutter setlog
trap 'rm -f /tmp/logentries$$' EXIT HUP INT QUIT TERM
cat >/tmp/logentries$$ <<EOF
------------------------------------------------------------------------
r2 | esr | 2011-11-30 16:43:52 +0000 (Wed, 30 Nov 2011) | 1 lines

Early comment tweak

------------------------------------------------------------------------
r4 | esr | 2011-11-30 16:46:05 +0000 (Wed, 30 Nov 2011) | 1 lines

Late comment tweak

------------------------------------------------------------------------
r5 | esr | 2011-12-05 11:27:20 +0000 (Mon, 05 Dec 2011) | 1 lines

If you see this in the output, the range restriction failed.

EOF
# shellcheck disable=SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" -r 2:4 -logentries=/tmp/logentries$$ setlog <vanilla.svn

