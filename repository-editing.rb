#!/usr/bin/env ruby

# This script creates repository-editing.html from
# repository-editing.adoc. This can be done using the command-line,
# but this script adds in a little plugin which creates anchors for
# each reposurgeon command.

# See https://github.com/asciidoctor/asciidoctor/issues/2717 for more
# information.

require 'asciidoctor'
require 'asciidoctor/extensions'

Asciidoctor::Extensions.register do
  # Process the Asciidoc AST looking for definition lists and making
  # them into links and anchors much like the links on section titles.
  # This plugin assumes that you're generating HTML, and it inserts
  # the generated HTML by creating pass blocks. This is not the best
  # implementation, but it's the simplest.
  tree_processor do
    process do |doc|
      # if the document doesn't have the `dtanchors` attribute, this
      # plugin does nothing.
      next unless doc.attr? 'dtanchors'
      (doc.find_by context: :dlist).each do |dlist|
        dlist.items.each do |(terms, _)|
          Array(terms).each do |term|
            term_id = Asciidoctor::Section.generate_id term.text, doc
            term.text = %(pass:[<a id="#{term_id}" class="anchor" href="##{term_id}"></a><a class="link" href="##{term_id}">]#{term.instance_variable_get :@text} pass:[</a>])
          end
        end
      end
      nil
    end
  end
end

Asciidoctor.convert_file 'repository-editing.adoc', :safe => :unsafe
