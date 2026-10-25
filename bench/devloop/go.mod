module github.com/bertrandkhe/orbit/bench/devloop

go 1.27

toolchain go1.27.3

require github.com/bertrandkhe/orbit v0.0.0

require (
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	golang.org/x/sys v0.0.0-20220715151400-c0bba94af5f8 // indirect
)

replace github.com/bertrandkhe/orbit => ../..
