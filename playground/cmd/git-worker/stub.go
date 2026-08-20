// Copyright (c) the go-tex authors.
// SPDX-License-Identifier: BSD-3-Clause

// Native build stub: the real entry point is wasm-only (see main.go). This keeps
// `go build ./...` and `go test ./...` (which cover the tagless handler) green on
// every host architecture.
//
//go:build !js || !wasm

package main

func main() {}
