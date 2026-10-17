// SPDX-License-Identifier: MIT
//
// modulecheck is the verification program of the module source archive
// (ADR-0025): it imports the core package of streamcrew and compiles
// against a module proxy that serves the archive from disk
// (GOPROXY=file://...). The release pipeline and the CI run it; a failure
// means the archive is not resolvable or not compilable.
//
// The go.mod pins the zero pseudo-version: the harness serves the archive
// under exactly that version, so no other resolution is involved.
package main

import (
	"fmt"

	"github.com/ripmav/streamcrew/core"
)

func main() {
	var opts core.Options
	fmt.Println("module check ok:", opts.Mode == "")
}
