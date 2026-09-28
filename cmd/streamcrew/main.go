// SPDX-License-Identifier: Apache-2.0

// Command streamcrew runs the headless core of streamcrew, a bot and
// automation service for live streams. Frontends such as the CLI/TUI, the
// desktop app and the web interface talk to it exclusively through its API.
//
// This is a placeholder until the skeleton of Roadmap Phase 1.3 exists: a kong
// CLI with the subcommands serve, version, config and doctor.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "streamcrew: not implemented yet (see docs/roadmap.md, Phase 1.3)")
	os.Exit(1)
}
