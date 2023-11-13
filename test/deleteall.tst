## Test if commands handling tree contents understand deleteall
set flag echo
read <deleteall.fi
set flag interactive
:13 manifest
[/^README/a] resolve
[/^README$/a] resolve
