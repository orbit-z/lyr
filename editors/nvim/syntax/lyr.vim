" Vim syntax file
" Language:    Lyr single-file components (.lyr)
" Reference:   editors/vscode/syntaxes/lyr.tmLanguage.json (same regions and
"              scoping), docs/reference/template-syntax.md
"
" Layers, all from Vim's bundled syntax files:
"   - HTML (syntax/html.vim) is the base of the template;
"   - Go (syntax/go.vim) for the frontmatter and inside {…};
"   - TypeScript for <script>, JavaScript for <script is:inline>, JSON for
"     <script type="application/json">, CSS for <style>.
" A missing bundled syntax only loses that layer's colors.

if !exists('main_syntax')
  if exists('b:current_syntax')
    finish
  endif
  let main_syntax = 'lyr'
endif

let s:cpo_save = &cpo
set cpo&vim

" s:Include loads syntax/{name}.vim into @{cluster}; it returns 0, without an
" error, when no such file is on 'runtimepath'.
function! s:Include(cluster, name) abort
  execute 'syntax cluster ' . a:cluster . ' contains=NONE'
  if empty(globpath(&runtimepath, 'syntax/' . a:name . '.vim', 0, 1))
    return 0
  endif
  unlet! b:current_syntax
  execute 'syntax include @' . a:cluster . ' syntax/' . a:name . '.vim'
  unlet! b:current_syntax
  return 1
endfunction

" HTML base. html.vim itself loads JavaScript and CSS; its <script>/<style>
" regions are replaced below.
runtime! syntax/html.vim
unlet! b:current_syntax
syntax case match

" html.vim lets @htmlPreproc (the template language) into quoted values,
" comments, scripts and styles; in Lyr those are literal, so they're
" redefined without it.
for s:group in ['htmlString', 'htmlValue', 'htmlComment', 'htmlCommentNested',
      \ 'htmlCommentError', 'javaScript', 'htmlScriptTag', 'htmlVbScript',
      \ 'javaScriptExpression', 'cssStyle', 'htmlCssDefinition']
  if hlexists(s:group)
    silent! execute 'syntax clear ' . s:group
  endif
endfor
unlet s:group
silent! syntax cluster htmlJavaScript remove=@htmlPreproc
syntax region htmlString contained start=+"+ end=+"+ contains=htmlSpecialChar
syntax region htmlString contained start=+'+ end=+'+ contains=htmlSpecialChar
syntax match  htmlValue  contained +=[\t ]*[^'"{ \t>][^ \t>]*+hs=s+1
syntax region htmlComment start=+<!--+ end=+--!\=>+ contains=@Spell
syntax region lyrDoctype start=+<![Dd][Oo][Cc][Tt][Yy][Pp][Ee]\>+ end=+>+

" Embedded languages.
call s:Include('lyrGo', 'go')
call s:Include('lyrTS', 'typescript')
call s:Include('lyrCSS', 'css')
call s:Include('lyrJSON', 'json')
" JavaScript is already loaded by html.vim; load it only when it isn't.
if hlexists('javaScriptStringD')
  syntax cluster lyrJS contains=@htmlJavaScript
else
  call s:Include('lyrJS', 'javascript')
endif

" Go inside the template ({…}, control headers, attribute values).
" Strings, runes and comments come from go.vim, so a } inside them never ends
" the expression; nested braces are balanced by lyrGoBraces. Backtick strings
" are template literals here (lyrTemplate), raw strings in the frontmatter.
syntax cluster lyrGoInner contains=lyrTemplate,lyrGoBraces,@lyrGo
syntax region lyrGoBraces contained transparent matchgroup=lyrGoBrace start=/{/ end=/}/

" Template literals: JavaScript escapes and ${…} pieces holding Go.
syntax region lyrTemplate contained matchgroup=lyrTemplateQuote start=/`/ end=/`/
      \ contains=lyrTemplateBadEscape,lyrTemplateEscape,lyrTemplateExpr
syntax match  lyrTemplateBadEscape contained /\\./
syntax match  lyrTemplateEscape contained /\\\%([\\`$nrtbfv'"]\|0\d\@!\|x\x\{2}\|u\x\{4}\|u{\x\+}\|$\)/
syntax region lyrTemplateExpr contained matchgroup=lyrTemplateDelim start=/\${/ end=/}/
      \ contains=@lyrGoInner

" {expr}, attribute values a={expr} and the shorthand {a}.
syntax region lyrExpr matchgroup=lyrBrace start=/{/ end=/}/ contains=@lyrGoInner

" {...attrs}
syntax region lyrSpread matchgroup=lyrBrace start=/{\ze\s*\.\.\./ end=/}/
      \ contains=lyrSpreadOp,@lyrGoInner
syntax match  lyrSpreadOp contained /\%({\s*\)\@<=\.\.\./

" Control flow. The head ({ plus the keyword) is one match, so it wins over
" go.vim's keywords; the rest of the header is Go.
syntax match  lyrBraceChar contained /[{}]/
syntax region lyrCond start=/{\ze\s*\%(if\|else\s\+if\|switch\|case\)\>/
      \ matchgroup=lyrBrace end=/}/ contains=lyrCondHead,@lyrGoInner
syntax match  lyrCondHead contained /{\s*\%(if\|else\s\+if\|switch\|case\)\>/
      \ contains=lyrBraceChar
syntax region lyrFor start=/{\ze\s*for\>/
      \ matchgroup=lyrBrace end=/}/ contains=lyrForHead,@lyrGoInner
syntax match  lyrForHead contained /{\s*for\>/ contains=lyrBraceChar
syntax match  lyrCondTag /{\s*\%(else\|default\)\s*}/ contains=lyrBraceChar
syntax match  lyrCondTag +{\s*/\s*\%(if\|switch\)\s*}+ contains=lyrBraceChar
syntax match  lyrForTag  +{\s*/\s*for\s*}+ contains=lyrBraceChar

" {/* … */}: a template comment.
syntax region lyrComment start=+{\s*/\*+ end=+\*/\s*}+ contains=@Spell

" Attributes. A component's props are all highlighted (lyrAttrName); HTML
" tags keep html.vim's attribute names. A directive's match starts at the
" blank before it (highlighted from the name, hs=s+1), so it wins over
" html.vim's keywords such as class, is and slot.
syntax match  lyrAttrName contained /[^[:space:]"'<>/={}`]\+/
syntax match  lyrDirective contained
      \ /\s\%(\%(set\|client\|server\|is\):\a[-[:alnum:]_]*\|class:list\|slot\)\%([[:space:]=/>]\|$\)\@=/hs=s+1
syntax cluster lyrHTMLAttrs contains=lyrDirective,htmlArg,htmlString,htmlValue,htmlSpecialChar,lyrExpr,lyrSpread,lyrComment
syntax cluster lyrAttrs contains=lyrAttrName,@lyrHTMLAttrs
syntax cluster htmlArgCluster add=lyrDirective

" Components: capitalized tags; <Self> and <Fragment> are built in, and
" <>…</> is a fragment.
syntax region lyrComponentTag start=+<\u+ end=+>+ contains=lyrComponentName,@lyrAttrs
syntax match  lyrComponentName contained +<\zs\u[[:alnum:]_]*+ contains=lyrBuiltinComponent
syntax match  lyrComponentEndTag +</\u[[:alnum:]_]*\s*>+ contains=lyrComponentEndName
syntax match  lyrComponentEndName contained +</\zs\u[[:alnum:]_]*+ contains=lyrBuiltinComponent
syntax keyword lyrBuiltinComponent contained Self Fragment
syntax match  lyrFragment +</\=>+

" A < that doesn't start a tag (a < b, <3, <=) is text.
syntax match  lyrTextLt +<\%([[:alpha:]/!>]\)\@!+

" <script> and <style>: opaque, {…} isn't Go inside them. A self-closed
" <script src="…" /> is an ordinary tag.
syntax region lyrScriptTag contained start=+<script\>+ end=+>+
      \ contains=htmlTagN,htmlTagError,@lyrHTMLAttrs
syntax region lyrStyleTag contained start=+<style\>+ end=+>+
      \ contains=htmlTagN,htmlTagError,@lyrHTMLAttrs
syntax region lyrScript start=+<script\>\_[^>]*\%(/\)\@1<!>+
      \ keepend end=+</script\_[^>]*>+me=s-1 contains=lyrScriptTag,@lyrTS
syntax region lyrScriptInline
      \ start=+<script\>\%(\_[^>]*\_s\)\=is:inline\>\_[^>]*\%(/\)\@1<!>+
      \ keepend end=+</script\_[^>]*>+me=s-1 contains=lyrScriptTag,@lyrJS
syntax region lyrScriptJSON
      \ start=#<script\>\_[^>]*\<type\s*=\s*["']\=application/\%(ld+\)\=json\>\_[^>]*\%(/\)\@1<!>#
      \ keepend end=+</script\_[^>]*>+me=s-1 contains=lyrScriptTag,@lyrJSON
syntax region lyrStyle start=+<style\>\_[^>]*\%(/\)\@1<!>+
      \ keepend end=+</style\_[^>]*>+me=s-1 contains=lyrStyleTag,@lyrCSS

" Inside html.vim's regions (tags, <head>, <b>, <h1>, links, …) too.
syntax cluster htmlPreproc add=lyrExpr,lyrSpread,lyrCond,lyrFor,lyrCondTag,lyrForTag,lyrComment
syntax cluster htmlPreproc add=lyrComponentTag,lyrComponentEndTag,lyrFragment,lyrTextLt
syntax cluster htmlPreproc add=lyrScript,lyrScriptInline,lyrScriptJSON,lyrStyle

" Go frontmatter: a --- on the first line, up to the next --- line.
syntax region lyrFrontmatter matchgroup=lyrFrontmatterDelim
      \ start=/\%^---\s*$/ end=/^---\s*$/ keepend contains=@lyrGo

" The frontmatter can only start on line 1; syncing from the start is cheap
" (typescript.vim does the same).
syntax sync clear
syntax sync fromstart

highlight default link lyrFrontmatterDelim  PreProc
highlight default link lyrDoctype           Comment
highlight default link lyrBrace             Delimiter
highlight default link lyrBraceChar         Delimiter
highlight default link lyrCondHead          Conditional
highlight default link lyrCondTag           Conditional
highlight default link lyrForHead           Repeat
highlight default link lyrForTag            Repeat
highlight default link lyrSpreadOp          Operator
highlight default link lyrComment           Comment
highlight default link lyrTemplate          String
highlight default link lyrTemplateQuote     String
highlight default link lyrTemplateEscape    SpecialChar
highlight default link lyrTemplateBadEscape Error
highlight default link lyrTemplateDelim     Delimiter
highlight default link lyrDirective         PreProc
highlight default link lyrAttrName          Type
highlight default link lyrComponentTag      Function
highlight default link lyrComponentEndTag   Identifier
highlight default link lyrComponentName     Type
highlight default link lyrComponentEndName  Type
highlight default link lyrBuiltinComponent  Structure
highlight default link lyrFragment          Function
highlight default link lyrScriptTag         Function
highlight default link lyrStyleTag          Function

let b:current_syntax = 'lyr'
if main_syntax ==# 'lyr'
  unlet main_syntax
endif

let &cpo = s:cpo_save
unlet s:cpo_save
