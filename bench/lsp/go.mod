module github.com/orbit-z/lyr/bench/lsp

go 1.27

toolchain go1.27.3

require github.com/orbit-z/lyr/bench/devloop v0.0.0

replace (
	github.com/orbit-z/lyr => ../..
	github.com/orbit-z/lyr/bench/devloop => ../devloop
)
