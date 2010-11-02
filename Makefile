#
# makefile for reposurgeon
#
VERS=$(shell sed <reposurgeon -n -e '/version=\(.*\)/s//\1/p')

SOURCES = README COPYING reposurgeon reposurgeon.xml reposurgeon.1 Makefile
SOURCES += control .shipper reposturgeon.gif

all: reposurgeon.1

reposurgeon.1: reposurgeon.xml
	xmlto man reposurgeon.xml

reposurgeon.html: reposurgeon.xml
	xmlto html-nochunks reposurgeon.xml

clean:
	rm -fr .rs/* test/.rs* *~ *.1 *.html MANIFEST SHIPPER.*
	rm -f typescript test/typescript

reposurgeon-$(VERS).tar.gz: $(SOURCES)
	@ls $(SOURCES) | sed s:^:reposurgeon-$(VERS)/: >MANIFEST
	@(cd ..; ln -s reposurgeon reposurgeon-$(VERS))
	(cd ..; tar -czvf reposurgeon/reposurgeon-$(VERS).tar.gz `cat reposurgeon/MANIFEST`)
	@(cd ..; rm reposurgeon-$(VERS))

version:
	@echo $(VERS)

check:
	cd test; make --quiet

dist: reposurgeon-$(VERS).tar.gz

release: reposurgeon-$(VERS).tar.gz reposurgeon.html
	shipper -u -m -t; make clean
