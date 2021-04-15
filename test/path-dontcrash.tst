## Test that the path command does not crash if called with only one argument that is not a valid verb
set relax
read <sample1.fi
path foo      # this should print an error message, not crash!
path foo bar  # this should give 'foo' as the unknown verb, not 'bar'!
=C path foo bar  # this too..
