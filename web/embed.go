// Package web embeds the built Svelte SPA so the server ships as one binary
// with no runtime file dependencies (SPEC §2.2, §4).
//
// The embedded tree is web/dist, which is Vite's build output. A placeholder
// index.html is committed so `go build` works on a checkout where the SPA has
// never been built; a real build overwrites it.
package web

import (
	"embed"
	"io/fs"
)

// Dist is the raw embedded build output, rooted at "dist/". Most callers want
// DistFS instead.
//
//go:embed all:dist
var Dist embed.FS

// DistFS returns the built SPA with the "dist/" prefix stripped, ready to hand
// to http.FileServerFS.
func DistFS() fs.FS {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		// dist is embedded at compile time, so this can only fail if the
		// embed directive above is wrong — a build defect, not a runtime
		// condition.
		panic("web: embedded dist subtree missing: " + err.Error())
	}
	return sub
}
