//go:build tools

// Package core pins build-time tool dependencies so `go mod tidy` keeps them.
// golang.org/x/mobile/bind is required by `gomobile bind` (make aar).
package core

import _ "golang.org/x/mobile/bind"
