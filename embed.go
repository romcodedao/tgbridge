// Package tgbridge holds files embedded into the tgbridge binary.
package tgbridge

import _ "embed"

// ExampleConfig is config.example.yaml, written by `tgbridge init` on first run.
//
//go:embed config.example.yaml
var ExampleConfig []byte
