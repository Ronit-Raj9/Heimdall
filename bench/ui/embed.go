// Package ui embeds the built Bifrost Bench web UI so the bench HTTP server can serve it
// from the binary with no files on disk.
//
// The UI is a Vite app in this folder. `npm run build` writes it to dist/. Until the first
// build, dist/ holds only a committed .gitkeep, which keeps this embed compiling; the server
// should treat a Dist without index.html as "UI not built".
package ui

import "embed"

// Dist is the built UI. Paths inside it start with "dist/", for example "dist/index.html";
// use fs.Sub(Dist, "dist") to serve it from the site root.
//
//go:embed all:dist
var Dist embed.FS
