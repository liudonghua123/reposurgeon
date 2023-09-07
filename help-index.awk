#!/bin/awk -f

# This script relies ON START-TOC and END-TOC being used to delimit portions of the
# input that should be scanned for sectioin headers, command definitions, and
# inclusions.
#
# Things it knows about:
# * Asciidoc section headers
# * Asciidoc file inclusions (each one becomes a topic)
# * Asciidoc hanging-definition syntax (each one becomes a topic)
# * START-TOC/END-TOC begfins or ends a span in which topic should be scanned for
# * TOPIC marks a following topic that is not a reposurgeon command.

function flush_chapter_toc() {
    if (intoc) {
	if (counters[3] == "") { # put a blank line after every chapter
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
}


BEGIN {
    intoc = 0

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
    flush_chapter_toc()
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
    #topicsuffix = ""
}
/TOPIC/ {
    topicsuffix = "*"
}
/[^:]::$/ {
    name = ($1 ~ /SELECTION/) ? $2 : $1
    sub(/[^a-z]+/, "", name)
    commands[maxcommand] = name topicsuffix
    maxcommand += 1
    topicsuffix = ""
}
/include::/ {
    split($1, parts, "/")
    split(parts[2], parts, ".")
    commands[maxcommand] = parts[1] topicsuffix
    maxcommand += 1
    topicsuffix = ""
}
/START-TOC/ {
    maxcommand = 1
    intoc = 1
    topicsuffix = ""
}
/END-TOC/ {
    flush_chapter_toc()
    intoc = 0
    topicsuffix = ""
}

END {
    print "  help{\"Starred topics are not commands.\", nil},"
    print "}"
}
