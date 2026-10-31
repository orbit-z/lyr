" Headless syntax tests for editors/nvim, run by run.sh in Neovim and Vim.
" Each check opens a .lyr file and asserts the syntax items at the first
" character of a needle:
"   [file, line, needle, expect]
"   line    literal text identifying the line (its first occurrence)
"   needle  literal text on that line (its first occurrence)
"   expect  'Group'  innermost syntax item is Group
"           '+Group' Group is somewhere in the syntax stack
"           '-Group' Group isn't in the syntax stack
"           '=Group' the item's highlight resolves to Group (Conditional, …)
" Failures and a summary go to $LYR_TEST_OUT; the editor exits 1 on failure.

let s:dir = expand('<sfile>:p:h')
let s:vscode = s:dir . '/../../vscode/test/'
let s:fails = []
let s:count = 0

function! s:Stack(l, c) abort
  return map(synstack(a:l, a:c), 'synIDattr(v:val, "name")')
endfunction

function! s:Check(file, line, needle, expect) abort
  let s:count += 1
  let l:where = fnamemodify(a:file, ':t') . ': ' . string(a:line) . ' @ ' . string(a:needle)
  call cursor(1, 1)
  let l:l = search('\V' . escape(a:line, '\'), 'cW')
  if l:l == 0
    call add(s:fails, l:where . ': line not found')
    return
  endif
  let l:c = stridx(getline(l:l), a:needle) + 1
  if l:c == 0
    call add(s:fails, l:where . ': needle not found')
    return
  endif
  let l:stack = s:Stack(l:l, l:c)
  let l:id = synID(l:l, l:c, 1)
  let l:kind = a:expect[0]
  if l:kind ==# '+'
    let l:ok = index(l:stack, a:expect[1:]) >= 0
  elseif l:kind ==# '-'
    let l:ok = index(l:stack, a:expect[1:]) < 0
  elseif l:kind ==# '='
    let l:ok = synIDattr(synIDtrans(l:id), 'name') ==# a:expect[1:]
  else
    let l:ok = synIDattr(l:id, 'name') ==# a:expect
  endif
  if !l:ok
    call add(s:fails, printf('%s: want %s, got %s (resolves to %s)', l:where, a:expect,
          \ join(l:stack, ' > '), synIDattr(synIDtrans(l:id), 'name')))
  endif
endfunction

" s:Open edits a file and checks it loaded as filetype lyr without errors.
function! s:Open(file) abort
  let v:errmsg = ''
  execute 'silent edit! ' . fnameescape(a:file)
  let s:count += 1
  if &filetype !=# 'lyr' || get(b:, 'current_syntax', '') !=# 'lyr'
    call add(s:fails, a:file . ': filetype=' . &filetype . ' syntax=' . get(b:, 'current_syntax', ''))
  endif
  if v:errmsg !=# ''
    call add(s:fails, a:file . ': error while loading: ' . v:errmsg)
  endif
endfunction

function! s:Run(file, checks) abort
  call s:Open(a:file)
  for c in a:checks
    call s:Check(a:file, c[0], c[1], c[2])
  endfor
endfunction

let s:all = s:dir . '/constructs.lyr'

if $LYR_TEST_MODE ==# 'bare'
  " No html.vim, go.vim, …: syntax/lyr.vim is reloaded with only this
  " plugin on 'runtimepath'. It must still load, with Lyr's own items.
  call s:Open(s:all)
  let &runtimepath = $LYR_PLUGIN
  let v:errmsg = ''
  set syntax=OFF
  set syntax=lyr
  let s:count += 1
  if v:errmsg !=# '' || get(b:, 'current_syntax', '') !=# 'lyr'
    call add(s:fails, 'bare: error while loading: ' . v:errmsg)
  endif
  for s:c in [
        \ ['---', '---', 'lyrFrontmatterDelim'],
        \ ['title := strings', 'title', 'lyrFrontmatter'],
        \ ['<p>{map[string]', 'map', 'lyrExpr'],
        \ ['<p>{map[string]', '{"a"', '+lyrGoBraces'],
        \ ['{if props.Open', 'if', '+lyrCondHead'],
        \ ['<Card client:visible', 'Card', 'lyrComponentName'],
        \ ['<Card client:visible', 'client', 'lyrDirective'],
        \ ['  .hero { color', 'hero', 'lyrStyle'],
        \ ['  .hero { color', '{', '-lyrExpr'],
        \ ['<html lang', 'html', '-htmlTag'],
        \ ]
    call s:Check(s:all, s:c[0], s:c[1], s:c[2])
  endfor
else
  call s:Run(s:all, [
        \ ['---', '---', 'lyrFrontmatterDelim'],
        \ ['---', '---', '=PreProc'],
        \ ['"strings"', '"strings"', '+goString'],
        \ ['type Props struct', 'struct', '+lyrFrontmatter'],
        \ ['type Props struct', 'struct', '+goDeclType'],
        \ ['Title string', 'string', 'goType'],
        \ ['var re = ', '{"', '+goString'],
        \ ['var re = ', '{"', '-lyrExpr'],
        \ ['raw := `a }', '`a', 'goRawString'],
        \ ['raw := `a }', '${b}', '-lyrTemplateExpr'],
        \ ['title := strings', 'title', 'lyrFrontmatter'],
        \ ['<!DOCTYPE html>', 'DOCTYPE', 'lyrDoctype'],
        \ ['<html lang', 'html', 'htmlTagName'],
        \ ['<html lang', 'lang', 'htmlArg'],
        \ ['<html lang', '"en"', 'htmlString'],
        \ ['<title>{title}', '{', 'lyrBrace'],
        \ ['<title>{title}', 'title}', '+lyrExpr'],
        \ ['<style>', 'style', '+lyrStyleTag'],
        \ ['.hero { color', 'hero', '+cssClassName'],
        \ ['.hero { color', '{', '-lyrExpr'],
        \ ['.hero { color', 'color', 'cssTextProp'],
        \ ['<h1 class="hero">', '{', 'lyrBrace'],
        \ ['<h1 class="hero">', 'title', 'lyrExpr'],
        \ ['<h1 class="hero">', '</h1>', 'htmlEndTag'],
        \ ['{/* note', '{/*', 'lyrComment'],
        \ ['{/* note', '{title}', 'lyrComment'],
        \ ['{/* note', '}', '=Comment'],
        \ ['<!-- {title}', '{title}', 'htmlComment'],
        \ ['<!-- {title}', '{title}', '-lyrExpr'],
        \ ['<p>{map[string]', 'map', 'goType'],
        \ ['<p>{map[string]', '{"a"', 'lyrGoBrace'],
        \ ['<p>{map[string]', '1}', '+lyrGoBraces'],
        \ ['<p>{map[string]', '["a"]', 'lyrExpr'],
        \ ['<p>{map[string]', '"a"]', 'goString'],
        \ ['<p>{map[string]', '} and {"', 'lyrBrace'],
        \ ['<p>{map[string]', ' and {"', '-lyrExpr'],
        \ ['<p>{map[string]', '"}"', 'goString'],
        \ ['<p>{map[string]', "'}'", 'goCharacter'],
        \ ['<p>{map[string]', ' end<', '-lyrExpr'],
        \ ['<p>{`/p/', '`/p/', 'lyrTemplateQuote'],
        \ ['<p>{`/p/', '/p/', 'lyrTemplate'],
        \ ['<p>{`/p/', '${', 'lyrTemplateDelim'],
        \ ['<p>{`/p/', 'props', '+lyrTemplateExpr'],
        \ ['<p>{`/p/', 'props', 'lyrTemplateExpr'],
        \ ['<p>{`/p/', '"x"', 'goString'],
        \ ['<p>{`/p/', ' tail', '-lyrExpr'],
        \ ['<p>{`a ${f(', 'y ', 'lyrTemplate'],
        \ ['<p>{`a ${f(', 'z}', '+lyrTemplateExpr'],
        \ ['<p>{`a ${f(', ' b ', 'lyrTemplate'],
        \ ['<p>{`a ${f(', '"}"', 'goString'],
        \ ['<p>{`a ${f(', ' c`', 'lyrTemplate'],
        \ ['<p>{`a ${f(', ' after', '-lyrExpr'],
        \ ['<p>{`esc', '\`', 'lyrTemplateEscape'],
        \ ['<p>{`esc', '\${', 'lyrTemplateEscape'],
        \ ['<p>{`esc', '\n', 'lyrTemplateEscape'],
        \ ['<p>{`esc', '\d', 'lyrTemplateBadEscape'],
        \ ['{if props.Open', '{', 'lyrBraceChar'],
        \ ['{if props.Open', 'if', 'lyrCondHead'],
        \ ['{if props.Open', 'if', '=Statement'],
        \ ['{if props.Open', 'props', 'lyrCond'],
        \ ['{if props.Open', 'len', 'goBuiltins'],
        \ ['{if props.Open', '> 0', 'lyrCond'],
        \ ['{if props.Open', '0}', 'goDecimalInt'],
        \ ['{if props.Open', '}', 'lyrBrace'],
        \ ['<i>open</i>', 'open', '-lyrCond'],
        \ ['{else if', 'else', 'lyrCondHead'],
        \ ['{else if', ' if', 'lyrCondHead'],
        \ ['{else if', '"x"', 'goString'],
        \ ['{else}', 'else', 'lyrCondTag'],
        \ ['{else}', '}', 'lyrBraceChar'],
        \ ['{/if}', '/if', 'lyrCondTag'],
        \ ['{/if}', '/if', '=Statement'],
        \ ['{for i, c', 'for', 'lyrForHead'],
        \ ['{for i, c', 'for', '=Statement'],
        \ ['{for i, c', 'range', 'goRepeat'],
        \ ['{/for}', 'for', 'lyrForTag'],
        \ ['{/for}', 'for', '=Statement'],
        \ ['<b>{i}{c}</b>', 'c}', 'lyrExpr'],
        \ ['{switch props.Title}', 'switch', 'lyrCondHead'],
        \ ['{switch props.Title}', 'props', 'lyrCond'],
        \ ['{case "a"', 'case', 'lyrCondHead'],
        \ ['{case "a"', '"b"', 'goString'],
        \ ['{case "a"', '<i>', '-lyrCond'],
        \ ['{default}', 'default', 'lyrCondTag'],
        \ ['{/switch}', 'switch', 'lyrCondTag'],
        \ ['<a href={`/p/', '`', 'lyrTemplateQuote'],
        \ ['<a href={`/p/', 'props', '+lyrTemplateExpr'],
        \ ['<a href={`/p/', 'title=', 'htmlArg'],
        \ ['<a href={`/p/', '{not go}', 'htmlString'],
        \ ['<a href={`/p/', '{not go}', '-lyrExpr'],
        \ ['<a href={`/p/', '3 ', 'htmlValue'],
        \ ['<a href={`/p/', '...', 'lyrSpreadOp'],
        \ ['<a href={`/p/', 'attrs', 'lyrSpread'],
        \ ['<a href={`/p/', 'open}', 'lyrExpr'],
        \ ['<a href={`/p/', '>link', '+htmlTag'],
        \ ['<div class:list', 'class:list', 'lyrDirective'],
        \ ['<div class:list', 'class:list', '=PreProc'],
        \ ['<div class:list', '"on"', 'goString'],
        \ ['<div class:list', 'set:html', 'lyrDirective'],
        \ ['<div class:list', 'HTML(', '+lyrExpr'],
        \ ['<div class:list', 'xlink', '-lyrDirective'],
        \ ['<Card client:visible', '<', 'lyrComponentTag'],
        \ ['<Card client:visible', 'Card', 'lyrComponentName'],
        \ ['<Card client:visible', 'Card', '=Type'],
        \ ['<Card client:visible', 'client:visible', 'lyrDirective'],
        \ ['<Card client:visible', '"200px"', 'htmlString'],
        \ ['<Card client:visible', 'server:defer', 'lyrDirective'],
        \ ['<Card client:visible', 'post', 'lyrAttrName'],
        \ ['<Card client:visible', 'p}', 'lyrExpr'],
        \ ['<Card client:visible', 'slot', 'lyrDirective'],
        \ ['<Self depth', 'Self', 'lyrBuiltinComponent'],
        \ ['<Self depth', 'depth=', 'lyrAttrName'],
        \ ['<Self depth', '1}', 'goDecimalInt'],
        \ ['<Fragment slot', 'Fragment', 'lyrBuiltinComponent'],
        \ ['<Fragment slot', 'slot', 'lyrDirective'],
        \ ['<Fragment slot', '<>', 'lyrFragment'],
        \ ['<Fragment slot', '</>', 'lyrFragment'],
        \ ['<Fragment slot', '</Fragment', 'lyrComponentEndTag'],
        \ ['</Card>', 'Card', 'lyrComponentEndName'],
        \ ['<p>a < b', '< b', 'lyrTextLt'],
        \ ['<p>a < b', ' b,', '-htmlTag'],
        \ ['<p>a < b', '<3', 'lyrTextLt'],
        \ ['<p>a < b', '<=', 'lyrTextLt'],
        \ ['<p>a < b', '&amp;', 'htmlSpecialChar'],
        \ ['<script>', 'script', '+lyrScriptTag'],
        \ ['const el', 'const', '+lyrScript'],
        \ ['const el', 'const', 'typescriptVariable'],
        \ ['const el', 'HTMLElement', '+typescriptTypeArguments'],
        \ ['if (el)', '{ el', '-lyrExpr'],
        \ ['if (el)', '${el', '-lyrTemplateExpr'],
        \ ['<script is:inline>', 'is:inline', 'lyrDirective'],
        \ ['window.x', 'window', '+lyrScriptInline'],
        \ ['window.x', '{ a', '-lyrExpr'],
        \ ['window.x', '1', 'javaScriptNumber'],
        \ ['<script type="application/json"', 'set:json', 'lyrDirective'],
        \ ['<script type="application/json"', 'list', '+lyrExpr'],
        \ ['application/ld+json', '"@type"', '+lyrScriptJSON'],
        \ ['application/ld+json', '"@type"', '+jsonKeyword'],
        \ ['<script src=', 'src', 'htmlArg'],
        \ ['<script src=', 'src', '-lyrScript'],
        \ ['<p>{after}', 'after', 'lyrExpr'],
        \ ['</html>', 'html', 'htmlTagName'],
        \ ])

  " The VS Code grammar's snapshot inputs.
  call s:Run(s:vscode . 'page.lyr', [
        \ ['import (', 'import', '+lyrFrontmatter'],
        \ ['Layout "../', '"../', 'goString'],
        \ ['if err != nil', 'if', 'goConditional'],
        \ ['<Layout title', 'Layout', 'lyrComponentName'],
        \ ['<Layout title', 'title', '=Type'],
        \ ['{/* a template', '{title}', 'lyrComment'],
        \ ['{if len(list) == 0}', 'if', 'lyrCondHead'],
        \ ['{else if len', 'else', 'lyrCondHead'],
        \ ['{for _, p := range', 'range', 'goRepeat'],
        \ ['<Card post={p} featured>', 'featured', 'lyrAttrName'],
        \ ['<Card post={p} featured>', '<em>', '+htmlItalic'],
        \ ['{"}"} and', '"}"', 'goString'],
        \ ['{"}"} and', "'}'", 'goCharacter'],
        \ ['{"}"} and', '`}`', 'lyrTemplateQuote'],
        \ ['{"}"} and', '}`}', 'lyrTemplate'],
        \ ['{"}"} and', '["a"]', 'lyrExpr'],
        \ ['{"}"} and', '</p>', 'htmlEndTag'],
        \ ['{x /* } */', '/*', 'goComment'],
        \ ['{x /* } */', '+ 1', 'lyrExpr'],
        \ ['<p>a < b, <3', '< b', 'lyrTextLt'],
        \ ['<!-- {title}', '{title}', 'htmlComment'],
        \ ['<p slot="footer">', 'slot', 'lyrDirective'],
        \ ['</Layout>', 'Layout', 'lyrComponentEndName'],
        \ ])
  call s:Run(s:vscode . 'script-style.lyr', [
        \ ['.hero { color', 'color', 'cssTextProp'],
        \ ['<style is:global>', 'is:global', 'lyrDirective'],
        \ ['body { margin', 'margin', '+lyrStyle'],
        \ ['querySelector<HTMLElement>', 'const', 'typescriptVariable'],
        \ ['alert(`hi ${el.id}`)', '${el', '-lyrTemplateExpr'],
        \ ['window.x', 'window', '+lyrScriptInline'],
        \ ['id="posts"', 'set:json', 'lyrDirective'],
        \ ['{"@type": "Blog"}', '"Blog"', '+lyrScriptJSON'],
        \ ['<script src="/x.js" />', 'src', 'htmlArg'],
        \ ['<p>{after}</p>', 'after', 'lyrExpr'],
        \ ])
  call s:Run(s:vscode . 'template-literal.lyr', [
        \ ['var re = regexp', '${raw}', 'goRawString'],
        \ ['<a href={`/fiction', 'props', '+lyrTemplateExpr'],
        \ ['<a href={`/fiction', 'aria-label', 'htmlArg'],
        \ ['<Card title={`Chapter', 'Chapter', 'lyrTemplate'],
        \ ['{if props.Slug == `a-', 'a-', 'lyrTemplate'],
        \ ['{case `draft`}', 'draft', 'lyrTemplate'],
        \ ['class:list={{`btn', 'btn-', 'lyrTemplate'],
        \ ['class:list={{`btn', '"on"', 'goString'],
        \ ['class:list={{`btn', '}>c', 'lyrBrace'],
        \ ['line ${two}', 'two', '+lyrTemplateExpr'],
        \ ['end`}>m</p>', 'end', 'lyrTemplate'],
        \ ['end`}>m</p>', '>m', '-lyrExpr'],
        \ ['<p>{`esc', '\0', 'lyrTemplateEscape'],
        \ ['<p>{`esc', '\u{1F600}', 'lyrTemplateEscape'],
        \ ['<p>{`esc', '\d', 'lyrTemplateBadEscape'],
        \ ['<p>{`a ${f(', ' c`', 'lyrTemplate'],
        \ ])
  call s:Run(s:vscode . 'attributes.lyr', [
        \ ['<div id="main"', "'box'", 'htmlString'],
        \ ['<div id="main"', '...', 'lyrSpreadOp'],
        \ ['<div id="main"', 'ToUpper', '+lyrExpr'],
        \ ['<Card client:media', 'client:media', 'lyrDirective'],
        \ ['<Comments server:defer', 'server:defer', 'lyrDirective'],
        \ ['<Comments server:defer', 'postID', 'lyrAttrName'],
        \ ['<Self depth', 'Self', 'lyrBuiltinComponent'],
        \ ['<slot name="aside">', 'slot', 'htmlTagName'],
        \ ['<my-element', 'some-attr', '-lyrDirective'],
        \ ])
  call s:Run(s:vscode . 'dyntag.lyr', [
        \ ['Tag := lyr.Cond', 'Tag', 'lyrFrontmatter'],
        \ ['<Tag {...props', 'Tag', 'lyrComponentName'],
        \ ['<Tag {...props', '...', 'lyrSpreadOp'],
        \ ['</Tag>', 'Tag>', 'lyrComponentEndName'],
        \ ['<html {...theme', '...', 'lyrSpreadOp'],
        \ ])
  call s:Run(s:vscode . 'switch.lyr', [
        \ ['{switch props.Status}', 'switch', 'lyrCondHead'],
        \ ['{/* the badge */}', 'badge', 'lyrComment'],
        \ ['{case "scheduled"', '"queued"', 'goString'],
        \ ['{default}', 'default', 'lyrCondTag'],
        \ ['{switch}', 'switch', 'lyrCondHead'],
        \ ['{case len(props', '> 10', 'lyrCond'],
        \ ['{switch v := props', 'type', '+lyrCond'],
        \ ['<p>{switcher}</p>', 'switcher', 'lyrExpr'],
        \ ])
  call s:Run(s:vscode . 'fragment.lyr', [
        \ ['<>', '<>', 'lyrFragment'],
        \ ['<ul>{for _, x', 'for', 'lyrForHead'],
        \ ['<ul>{for _, x', '<><li>', 'lyrFragment'],
        \ ['<ul>{for _, x', '</>{/for}', 'lyrFragment'],
        \ ['<ul>{for _, x', '/for', 'lyrForTag'],
        \ ['<p>a < b and', '< b', 'lyrTextLt'],
        \ ])
endif

" Timing: open a generated file of $LYR_TEST_LINES lines (from constructs.lyr)
" and highlight all of it.
if $LYR_TEST_LINES !=# '' && $LYR_TEST_MODE !=# 'bare'
  let s:body = readfile(s:all)
  let s:fm = index(s:body, '---', 1)
  let s:lines = s:body[: s:fm]
  while len(s:lines) < str2nr($LYR_TEST_LINES)
    call extend(s:lines, s:body[s:fm + 1 :])
  endwhile
  let s:big = tempname() . '.lyr'
  call writefile(s:lines, s:big)
  let s:start = reltime()
  call s:Open(s:big)
  for s:l in range(1, line('$'))
    call synID(s:l, 1, 1)
  endfor
  " The last line, as a jump to the end would (sync from the start).
  call synstack(line('$'), 1)
  let s:ms = reltimefloat(reltime(s:start)) * 1000
  call delete(s:big)
  let s:timing = printf('opened and highlighted %d lines in %.1f ms', line('$'), s:ms)
endif

let s:out = []
if !empty(s:fails)
  call extend(s:out, map(copy(s:fails), '"FAIL " . v:val'))
endif
call add(s:out, printf('%s: %d checks, %d failed', has('nvim') ? 'nvim' : 'vim', s:count, len(s:fails)))
if exists('s:timing')
  call add(s:out, s:timing)
endif
call writefile(s:out, $LYR_TEST_OUT)
if empty(s:fails)
  qall!
endif
cquit!
