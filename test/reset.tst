## Reset tests
set echo
set relax
read <be-bookmarks.fi
=R index
reset move D :6
reset delete A
reset rename B Z
27 reset move master :10
=R index

# error: unknown reset name
reset delete X
# error: move multiple resets
reset move master :15
# error: non-singleton target
reset move D :6,:10,:15
# error: empty new name
reset rename Z 
# error: reference collision
reset rename Z D
# error: bogus verb
reset fizzle Z 
