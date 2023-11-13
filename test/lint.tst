## Make sure the lint command works
set flag echo
set flag quiet
read <bs.fi
lint
=Q list
read <lint.svn
lint
=Q list
