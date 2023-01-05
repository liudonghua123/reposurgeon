#!/bin/sh
## Test that without modifiers it's a faithful copy
stem=vanilla	# Any Subversion dump we plug in here should make empty output
#shellcheck disable=SC2094,SC2086
${REPOCUTTER:-repocutter} -q -t "$(basename $0)" select <"${stem}.svn" 2>&1 | diff "${stem}.svn" -


