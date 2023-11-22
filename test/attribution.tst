## Test attribute manipulation
set flag relax
set flag echo

# error: no repo
attribute

read <multitag.fi

# error: parse failure
attribute missing "quotation mark

# error: no event selection
attribute delete

# error: no commits or tags selected
=R | =B attribute

# error: unrecognized action
1..$ attribute bogus

:2..:4 attribute 2 resolve
:2..:4 attribute $ resolve
:2..:4 attribute 1..$ resolve
:2..:4 attribute 1,$ resolve
:2..:4 attribute 1 | 2 resolve
:2..:4 attribute 1 & (2) resolve
:2..:4 attribute ~2 resolve
:2..:4 attribute @min(1..$) resolve
:2..:4 attribute @max(1..$) resolve
:2..:4 attribute @amp(1) resolve
:2..:4 attribute @pre(2) resolve
:2..:4 attribute @suc(1) resolve
:2..:4 attribute @srt(2,1) resolve
1..$ attribute @amp(~1) resolve label

# error: bogus selection (tag has only "tagger" attribution at index 1)
10 attribute 2 resolve

1..$ attribute =C resolve committer only
1..$ attribute =A resolve author only
1..$ attribute =T resolve tagger only
1..$ attribute =CAT resolve all

# error: bogus visibility flag
1..$ attribute =X resolve

@min(=C) attribute /Julien/ resolve match any
@min(=C) attribute /Julien/n resolve match name
@min(=C) attribute /frnchfrgg/e resolve match email
@min(=C) attribute /Julien/e resolve name not match email
@min(=C) attribute /frnchfrgg/n resolve email not match name
@min(=C) attribute /nomatch/ resolve no match

# error bogus regex match flag
@min(=C) attribute /Julien/x resolve

attribute
attribute show
1..$ attribute show
=C attribute 2 show
=T attribute 1 show
@max(=C) attribute =A show
# empty attribute selection
@max(=T) attribute =A show

#error: incorrect number of arguments
attribute show bogus

:2 attribute show
:2 attribute =C set 2017-03-21T01:23:45Z
:2 attribute show
:2 attribute =C set sunshine@sunshineco.com
:2 attribute show
:2 attribute =C set "Eric Sunshine"
:2 attribute show
:2 attribute @min(=A) set "1234567890 +0500" sunshine@sunshineco.com "Eric Sunshine"
:2 attribute show
:2 attribute =A set "Eric S. Raymond" esr@thyrsus.com
:2 attribute show
:2 attribute =A set sunshine@sunshineco.com 2017-03-21T01:23:45Z
:2 attribute show

# error: incorrect number of arguments or repeated arguments
1..$ attribute set
1..$ attribute set Name email@example.com "1234567890 +0500" junk
1..$ attribute set Name1 email@example.com Name2
1..$ attribute set email1@example.com Name email2@example.com
1..$ attribute set "1234567890 +0500" Name 2017-03-21T01:23:45Z

:5 attribute show
:5 attribute =A delete
:5 attribute show
# no attribute selection: delete all authors
:4 attribute show
:4 attribute delete
:4 attribute show
# multiple events
1..$ attribute show
1..$ attribute =A delete
1..$ attribute show

# error: incorrect number of arguments
1..$ attribute delete bogus
# error: no event selection
attribute =A delete
# error: cannot delete mandatory committer or tagger
@max(=C) attribute =C delete
@max(=T) attribute =T delete

:2 attribute show
:2 attribute append "Eric S. Raymond" esr@thyrsus.com "1234567890 +0500"
:2 attribute show
:2 attribute append "Eric Sunshine" sunshine@sunshineco.com 2017-03-21T01:23:45Z
:2 attribute show
:2 attribute /sunshine/ & =A prepend "Micky Mouse" toon@disney.com 1979-04-01T12:12:12Z
:2 attribute show
:2 attribute /esr/e prepend Example example@example.com "1234567890 +0500"
:2 attribute show

# error: incorrect number of arguments
1..$ attribute prepend
1..$ attribute append Name email@example.com "1234567890 +0500" junk
# error: no event selection
attribute =A prepend
# error: cannot add committer or tagger (only 1 allowed)
@max(=C) attribute =C prepend "Eric Sunshine" sunshine@sunshineco.com 2017-03-21T01:23:45Z
@max(=T) attribute =T append "Eric Sunshine" sunshine@sunshineco.com 2017-03-21T01:23:45Z
# error: cannot add author to tag
@max(=T) attribute append "Eric Sunshine" sunshine@sunshineco.com 2017-03-21T01:23:45Z

:2 attribute show
:2 attribute =A prepend "Eric Sunshine"
:2 attribute show
:2 attribute =A append esr@thyrsus.com
:2 attribute show
:2 attribute /disney/e append Pluto othertoon@disney.com
:2 attribute show

# infer author name and date from committer
:4 attribute show
:4 attribute append frnchfrgg@free.fr
:4 attribute show

# error: unable to infer name, email
:4 attribute append 2017-03-21T01:23:45Z
:4 attribute prepend Nobody
:4 attribute append nobody@nowhere.com
