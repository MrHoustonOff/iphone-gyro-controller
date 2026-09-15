package web

import (
	_ "embed"
)

// IndexHTML contains the embedded web client markup and scripts.
//
//go:embed index.html
var IndexHTML []byte
