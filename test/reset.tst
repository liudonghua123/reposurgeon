## Reset tests
set flag echo
set flag relax
read <be-bookmarks.fi
=R list index
move reset D :6
delete reset A
rename reset B Z
27 move reset master :10
=R list index

# error: unknown reset name
delete reset X
# error: move multiple resets
move reset master :15
# error: non-singleton target
move reset D :6,:10,:15
# error: empty new name
rename reset Z 
# error: reference collision
rename reset Z D
# error: bogus object type
move fizzle Z :15
