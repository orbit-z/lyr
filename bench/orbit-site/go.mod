module example.com/orbitbench

go 1.27

toolchain go1.27.3

tool github.com/orbit-z/orbit/cmd/orbit

require github.com/orbit-z/orbit v0.0.0

require (
	github.com/evanw/esbuild v0.28.2 // indirect
	github.com/fsnotify/fsnotify v1.10.1 // indirect
	github.com/tdewolff/parse/v2 v2.8.16 // indirect
	golang.org/x/sys v0.13.0 // indirect
)

replace github.com/orbit-z/orbit => ../..
