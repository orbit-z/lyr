-- The Lyr language server, for Neovim 0.11+: vim.lsp.enable('lyr').
-- Add settings with vim.lsp.config('lyr', { … }); see docs/editor-setup.md.
return {
  cmd = { 'lyr', 'lsp' },
  filetypes = { 'lyr' },
  root_markers = { 'go.mod' },
}
