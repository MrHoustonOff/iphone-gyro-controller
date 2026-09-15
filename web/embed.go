package web

import (
	_ "embed"
)

// IndexHTML contains the embedded phone controller web client.
//
//go:embed index.html
var IndexHTML []byte

// VisHTML contains the embedded Three.js 3D orientation visualizer.
//
//go:embed vis.html
var VisHTML []byte
