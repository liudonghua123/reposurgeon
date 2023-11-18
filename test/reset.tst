## Reset tests
set flag echo
set flag relax
read <be-bookmarks.fi
=R list index
reset move D :6
delete reset A
rename reset B Z
27 reset move master :10
=R list index

# error: unknown reset name
delete reset X
# error: move multiple resets
reset move master :15
# error: non-singleton target
reset move D :6,:10,:15
# error: empty new name
rename reset Z 
# error: reference collision
rename reset Z D
# error: bogus verb
reset fizzle Z 
