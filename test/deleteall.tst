## Test if commands handling tree contents understand deleteall
set flag echo
read <deleteall.fi
set flag interactive
:13 list manifest
[/^README/a] resolve
[/^README$/a] resolve
