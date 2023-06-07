#!/bin/sh
## Test repocutter swapcheck
# FIXME: repocutter swapcheck test is incomplete
# Needs tests for (1) toplevwl copy with standard layout
# undeneath, (2) standard layout buried more than one level deep.

# shellcheck disable=SC1091
. ./common-setup.sh

repository init svn /tmp/testsvn$$

repository mkdir crossflight

repository mkdir crossflight/src

# shellcheck disable=SC2086
repository export "swapcheck test load" | ${REPOCUTTER:-repocutter} -q -t "$(basename $0)" swapcheck 2>&1










