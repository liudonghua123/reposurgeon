## Test that the list command does not crash if called with only one argument that is not a valid verb
set flag relax
read <sample1.fi
list foo      # this should print an error message, not crash!
list foo bar  # this should give 'foo' as the unknown verb, not 'bar'!
=C list foo bar  # this too..
