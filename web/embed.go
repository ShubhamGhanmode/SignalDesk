package web

import "embed"

//go:embed index.html app.js styles.css favicon.svg
var Files embed.FS
