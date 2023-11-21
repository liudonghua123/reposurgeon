## Test selection sets
set flag relax
read <testrepo.fi
set flag echo
set flag interactive
1,3 resolve
1..3 resolve
:2,:4 resolve
:2..:4 resolve
3,1 resolve
=C resolve
@min(=C) resolve 
@max(=C) resolve
@par(35) resolve
@chn(3) resolve
@anc(14) resolve
@dsc(9) resolve
@pre(=C) resolve
@suc(14) resolve
@srt(3,1) resolve
@rev(3,4,1) resolve
# Bogus inputs
1.3 resolve
1...3 resolve

# Create some tags to test tag selection
@max(=C) create tag tag1test
# Test selection default
create tag tag2test

# Test tag filtering by name
=T&/tag1/n list inspect
