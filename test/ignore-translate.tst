## test ignore (defaults generation, rename, translation).
read <min.fi
set flag interactive
prefer bzr
ignores --defaults
# Next line should reveal a generated ignore blob and its fileop
:2,:3 inspect
prefer hg
ignores --rename --translate
clear flag interactive
write -

