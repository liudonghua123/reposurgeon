#!/bin/awk -f

# If you change the number or order of the chapters in the manual,
# this script needs to be updated to match.

BEGIN {
    # Currently Chapter 6 General command syntax has no commands in it,
    # but there are some relevant help topics that we should include. It
    # gets a special case with hand-written TOC entries.
    GENERAL_COMMAND_SYNTAX = 6

    # Chapters 7 through 12 currently document all the commands, while
    # chapters before and after this section use definition lists for
    # other purposes. Since we don't currently want those to show up in
    # the TOC, we simply don't print anything out for them.
    START_TOC = 7
    END_TOC = 12

    print "package main"
    print ""
    print "type help struct {"
    print "	title    string"
    print "	commands []string"
    print "}"
    print ""
    print "var _Helps = []help{"
}

/^=+/ {
    if (counters[2] == GENERAL_COMMAND_SYNTAX && counters[3] == "") {
        print ""
        print "\thelp{\"6. General command syntax\", nil},"
        print "\thelp{\"  1. Regular Expressions\", []string{\"regexp\"}},"
        print "\thelp{\"  2. Selection syntax\", []string{\"selection\", \"functions\"}},"
        print "\thelp{\"  3. Command syntax\", []string{\"syntax\"}},"
        print "\thelp{\"  4. Redirection and shell-like features\", []string{\"redirection\"}},"
    } else if (counters[2] >= START_TOC && counters[2] <= END_TOC) {
        if (counters[3] == "" && counters[2] > 6) { # put a blank line after every chapter
            print ""
        }
        if (maxcommand == 1) { # if there are no commands, the generated command list will be nil
            temp = "nil"
        } else { # otherwise, it'll be a comma-separated list of strings
            temp = "[]string{"
            for (i = 1; i < maxcommand; i++) {
                if (i > 1) {
                    temp = temp ", "
                }
                temp = temp "\"" commands[i] "\""
            }
            temp = temp "}"
        }
        if (counters[3] == "" || (counters[3] != "" && maxcommand > 1)) { # only print chapter headings or sections with commands
            print "\thelp{\"" indentation counters[depth] "." title "\", " temp "},"
        }
    }
    maxcommand = 1
    delete commands
    depth = length($1)
    title = ""
    for (i = 2; i <= NF; i++) {
        title = title " " $i
    }
    delete oldcounters
    for (i in counters) {
        oldcounters[i] = counters[i]
    }
    delete counters
    for (i = 1; i < depth; i++) {
        counters[i] = oldcounters[i]
    }
    counters[depth] = oldcounters[depth] + 1
    indentation = ""
    for (i = 2; i < depth; i++) {
        indentation = indentation "  "
    }
}
/[^:]::$/ {
    name = ($1 ~ /SELECTION/) ? $2 : $1
    sub(/[^a-z]+/, "", name)
    commands[maxcommand] = name
    maxcommand += 1
}
/include::/ {
    split($1, parts, "/")
    split(parts[2], parts, ".")
    if (parts[1] != "options") { # we include options.adoc, but it's not a command
        commands[maxcommand] = parts[1]
        maxcommand += 1
    }
}

END {
    print "}"
}
