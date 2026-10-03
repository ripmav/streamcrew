// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ripmav/streamcrew/internal/app"
	"github.com/ripmav/streamcrew/internal/commandfile"
	"github.com/ripmav/streamcrew/internal/domain/command"
)

type schemaCmd struct {
	Export schemaExportCmd `cmd:"" help:"Write the JSON Schema of commands as code into a directory."`
}

type schemaExportCmd struct {
	Dir string `default:"schemas" type:"path" env:"-" help:"Directory of the schema file, created if needed. Default: ${default}." placeholder:"DIR"`
}

// Run writes the JSON Schema of the files of commands as code
// (commands-as-code.md, B30). It needs neither the configuration nor a
// profile.
func (c schemaExportCmd) Run(e *Env) error {
	reg, err := app.ActionCatalog()
	if err != nil {
		return err
	}
	s, err := commandfile.Schema(reg.Descriptors(), command.RequirementCatalog())
	if err != nil {
		return err
	}
	out, err := commandfile.SchemaJSON(s)
	if err != nil {
		return fmt.Errorf("encode schema: %w", err)
	}
	if err := os.MkdirAll(c.Dir, 0o750); err != nil {
		return err
	}
	path := filepath.Join(c.Dir, commandfile.SchemaFile)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.Stdout, "wrote %s\n", path)
	return err
}
