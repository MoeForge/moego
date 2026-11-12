module github.com/MoeForge/moego/bench/regexp

go 1.27.1

require (
	github.com/MoeForge/moego v0.0.0
	github.com/coregx/coregex v0.12.30
	github.com/grafana/regexp v0.0.0-20250905093917-f7b3be9d1853
	github.com/wasilibs/go-re2 v1.13.0
)

require (
	github.com/coregx/ahocorasick v0.3.1 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/MoeForge/moego => ../..
