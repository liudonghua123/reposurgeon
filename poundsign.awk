# Massage URLs for use in asciidoc. Part of the documrntation toolchain
BEGIN { inside = 0 }
/^----/ { inside = !inside }
/^http.*]$/ { print }
!/^http.*]$/ { if (!inside) { gsub("#", "+#+", $0); } print $0 }
