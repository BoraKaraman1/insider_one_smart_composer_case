// Package schemas embeds the JSON Schemas that every Claude output and every
// human edit is validated against. The files are the contract; see
// docs/segment-rules.md for the segment rule semantics.
package schemas

import "embed"

// FS holds every *.schema.json file in this directory.
//
//go:embed *.schema.json
var FS embed.FS
