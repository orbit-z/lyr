-- Starts `lyr lsp` for .lyr files (lsp/lyr.lua), on Neovim 0.11 and later.
-- Plugins load after init.lua, so to opt out set vim.g.lyr_lsp = false
-- there (a vim.lsp.enable('lyr', false) in init.lua would be undone here).
-- Vim built with +lua may source this file: its Lua has no vim.lsp.
if vim.lsp == nil or vim.lsp.enable == nil or vim.fn.has('nvim-0.11') ~= 1 then
  return
end
if vim.g.lyr_lsp == false or vim.g.lyr_lsp == 0 then
  return
end
vim.lsp.enable('lyr')
