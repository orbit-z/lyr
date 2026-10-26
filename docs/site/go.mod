module github.com/bertrandkhe/orbit/docs/site

go 1.27

toolchain go1.27.3

tool github.com/bertrandkhe/orbit/cmd/orbit

require (
	github.com/bertrandkhe/orbit v0.0.0
	github.com/yuin/goldmark v1.8.6
)

require (
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	golang.org/x/sys v0.13.0 // indirect
)

replace github.com/bertrandkhe/orbit => ../..
