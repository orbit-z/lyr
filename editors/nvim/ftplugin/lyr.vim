" Vim filetype plugin for Lyr (.lyr) single-file components.

if exists('b:did_ftplugin')
  finish
endif
let b:did_ftplugin = 1

let s:cpo_save = &cpo
set cpo&vim

" Template comments are HTML comments (editors/vscode/language-configuration.json).
setlocal commentstring=<!--\ %s\ -->
setlocal comments=s:<!--,m:\ \ \ \ ,e:-->

" matchit: % jumps between control-flow blocks, HTML tags and comments.
let b:match_ignorecase = 0
let b:match_words = '<!--:-->,'
      \ .. '{\s*if\>:{\s*else\>:{\s*/\s*if\s*},'
      \ .. '{\s*for\>:{\s*/\s*for\s*},'
      \ .. '{\s*switch\>:{\s*\%(case\>\|default\s*}\):{\s*/\s*switch\s*},'
      \ .. '<\@<=\([^/!> \t][^ \t>/]*\)\%(\_[^>]*[^/]\)\=>:<\@<=/\1>'

let b:undo_ftplugin = 'setlocal commentstring< comments<'
      \ .. ' | unlet! b:match_ignorecase b:match_words'

let &cpo = s:cpo_save
unlet s:cpo_save
