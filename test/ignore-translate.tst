## test ignore (defaults generation, rename, translation).
read <min.fi
set flag interactive
sourcetype git
prefer bzr
ignores --defaults
# Next line should reveal a generated ignore blob and its fileop
:2,:3 list inspect
prefer hg
ignores --translate
clear flag interactive
write -

