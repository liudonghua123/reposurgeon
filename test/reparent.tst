## Reparenting parents with and w/o tree preservation
set flag echo
set flag relax
read <simple.fi
set flag interactive
127 list inspect
127..$ list manifest
127,29 reparent
127 list inspect
127..$ list manifest
129,3 reparent --rebase
129 list inspect
129 list manifest

129 reparent
@par(129) resolve parents of root commit
129,127,3 reparent
@par(129) resolve parents of octopus merge

# this next one should fail because it would create a cycle
:123,:121 reparent --use-order
:121 list inspect
:121 list manifest
# swap the order of :123 and :121
:119,:123 reparent --use-order
:123 list inspect
:123 list manifest
(:119..:123)|(:119..:121) list index
:123,:121 reparent --use-order
:121 list inspect
:121 list manifest
(:119..:123)|(:119..:121) list index
