// SPDX-License-Identifier: MIT

// Package web holds the checked-in bundle of the overlay runtime
// (Code-ADR-0016): the TypeScript source in ../runtime/src is bundled by
// go generate with the pinned esbuild version, and the result is served
// embedded by the overlay server.
package web

import _ "embed"

// Bundle is the bundled overlay runtime (single file, minified).
//
//go:embed bundle.js
var Bundle string
