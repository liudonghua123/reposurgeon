## Test the add primitive
set flag relax
read <sample2.fi
:15 add D .gitignore
:17 add M 100755 :9 hello
:19 add M 120000 :9 hello
:21 add M 100644 :9 hello
# Next one is expected to fail
:21 add M 100444 :9 hello
# Next one is expected to fail
:8 add C .gitignore wibble
:8 add C README README-STASH
write -
