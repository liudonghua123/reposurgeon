#!/bin/sh
# Create artificial test load containing svn:ignore or
# svn:global-ignores.  Takes the property suffix - ignores or
# global-ignores - as first argument. Writes the stream to stdout.
#
# Normally global-ignores is a property that is created and interpreted
# on the client side only.  Forcing it with a propset is a bit
# perverse. Nevertheless we've had a request to handle this case
#
# This is a GENERATOR

# shellcheck disable=SC1091
. ./common-setup.sh

set -e

dump=yes
verbose=null
while getopts v opt
do
    case $opt in
	v) verbose=stdout; dump=no;;
	*) echo "not ok - $0: unknown flag $opt"; exit 1;;
    esac
done
# shellcheck disable=SC2004
shift $(($OPTIND - 1))

if [ "$1" != "ignore" ] && [ "$1" != "global-ignores" ]
then
    echo "not ok - $0: invalid first argument, should be an ignore-type property"
    exit 1
fi

if [ -n "$2" ] && [ "$2" != "copy" ]
then
    echo "not ok - $0: invalid second argument, should be empty or 'copy'"
    exit 1
fi

{
    repository init svn

    mkdir -p trunk/subdir
    svn add trunk

    echo "this file should be versioned" > trunk/keepme.txt
    echo "this file should also be versioned" > trunk/subdir/keepme2.txt
    echo "this third file should also be versioned" > trunk/keepme.bar
    echo "this file should be ignored" > trunk/ignoreme.foo
    if [ "$1" = "ignore" ]
    then
	echo "this file should not be ignored" > trunk/subdir/keepme.foo
    else
	echo "this file should also be ignored" > trunk/subdir/ignoreme.foo
    fi
    echo "this third file should also be ignored" > trunk/subdir/ignoreme.bar

    #svn status
    # should include *.foo file:
    # A       trunk
    # ?       trunk/ignoreme.foo
    # ?       trunk/keepme.bar
    # ?       trunk/keepme.txt
    # A       trunk/subdir
    # ?       trunk/subdir/ignoreme.foo
    # ?       trunk/subdir/keepme.bar
    # ?       trunk/subdir/keepme2.txt

    svn propset "svn:$1" "*.foo" trunk
    svn propset "svn:$1" "*.bar" trunk/subdir

    #svn status
    # should _not_ list *.foo file:
    # A       trunk
    # ?       trunk/keepme.bar
    # ?       trunk/keepme.txt
    # A       trunk/subdir
    # ?       trunk/subdir/keepme2.txt

    # shellcheck disable=SC2035
    svn add * --force
    # should only add *.txt and toplevel *.bar
    # A         trunk/keepme.bar
    # A         trunk/keepme.txt
    # A         trunk/subdir/keepme2.txt

    svn commit -m "Test svn:$1 property."

    if [ "$2" = "copy" ]
    then
	mkdir branches
	svn add branches
	svn copy trunk branches/newbranch
	svn commit -m "Test if a branch copy preserves the properties"
    fi
    repository wrap

    # test that the property is stored in the repository by using a new clean checkout
    svn checkout "file://$(pwd)" test-checkout2-$$
    tapcd test-checkout2-$$
    echo "ignored" > trunk/something.foo
    echo "ignored" > trunk/subdir/something.foo
    echo "ignored" > trunk/subdir/something.bar

    # svn status
    # should return empty
} >"/dev/${verbose}" 2>&1

# shellcheck disable=2010
if [ "$dump" = yes ]
then
    repository export "svn:$1 property-setting example"
fi

# end
