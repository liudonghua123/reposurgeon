#!/usr/bin/ruby
require 'asciidoctor'
require 'asciidoctor/extensions'

Asciidoctor::Extensions.register do
  tree_processor do
    process do |doc|
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
