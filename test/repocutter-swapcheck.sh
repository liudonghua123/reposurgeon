#!/bin/sh
## Test repocutter swapcheck

# shellcheck disable=SC1091
. ./common-setup.sh

repository init svn /tmp/testsvn$$

repository mkdir crossflight            # This is OK
repository mkdir crossflight/src        # This should be reported

repository mkdir toodeep                # Would be OK if not for standard layout buried underneath
repository mkdir toodeep/proj           # Should be reported
repository mkdir toodeep/proj/trunk     # trunk is one level too deep, should be reported
repository mkdir toodeep/proj/tags      # tags is one level too deep, should be reported

repository mkdir trunk                  # Should not be reported
repository mkdir tags                   # Should not be reported
repository mkdir branches               # Should not be reported

# shellcheck disable=SC2086
repository export "swapcheck test load" | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapcheck 2>&1










