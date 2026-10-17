// SPDX-License-Identifier: MIT

package cli

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ripmav/streamcrew/internal/apitoken"
	"github.com/ripmav/streamcrew/internal/buildinfo"
	"github.com/ripmav/streamcrew/internal/profile"
)

// diagCmd groups the diagnostics of the core.
type diagCmd struct {
	Bundle diagBundleCmd `cmd:"" help:"Write a diagnostics bundle for bug reports; secrets are masked."`
}

type diagBundleCmd struct {
	// Out is the path of the bundle; empty is streamcrew-diag-<time>.zip
	// in the current directory.
	Out string `help:"The path of the bundle; empty is streamcrew-diag-<time>.zip in the current directory." placeholder:"FILE"`
}

// Run writes the bundle: the version, the configuration, the paths, the
// profiles, the API tokens without their hashes, the runtime file of a
// running core and the log files. No secrets go into the bundle (ADR-0011).
func (c diagBundleCmd) Run(ctx context.Context, e *Env) error {
	cfg, err := e.resolve()
	if err != nil {
		return err
	}
	out := c.Out
	if out == "" {
		out = "streamcrew-diag-" + time.Now().Format("20060102-150405") + ".zip"
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	add := func(name string, write func(w io.Writer) error) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if err := write(w); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	fail := func(err error) error {
		_ = zw.Close()
		_ = f.Close()
		// A failed bundle is no bundle: the partial file is removed.
		_ = os.Remove(out)
		return err
	}

	if err := add("README.txt", func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "streamcrew diagnostics bundle\nCreated: %s\nSystem: %s/%s\n\nThis bundle contains logs, versions and the configuration of\nstreamcrew. It does not contain secrets: no API token values or\nhashes, no secret keys, no platform tokens and no TLS keys.\n",
			time.Now().Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := add("version.txt", func(w io.Writer) error {
		return writeJSON(w, buildinfo.Read())
	}); err != nil {
		return fail(err)
	}
	if err := add("config.yaml", func(w io.Writer) error {
		out, err := yaml.Marshal(cfg.View())
		if err != nil {
			return err
		}
		_, err = w.Write(out)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := add("paths.txt", func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "config_file: %s\ndata_dir: %s\nlog_dir: %s\n", e.File.Path(), cfg.DataDir, cfg.LogDir())
		return err
	}); err != nil {
		return fail(err)
	}
	if err := add("profiles.txt", func(w io.Writer) error {
		profiles, err := profile.NewManager(cfg.DataDir).List(ctx)
		if err != nil {
			return err
		}
		for _, p := range profiles {
			marker := " "
			if p.Active {
				marker = "*"
			}
			if _, err := fmt.Fprintf(w, "%s %s (%s)\n", marker, p.Name, p.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	if err := add("api-tokens.txt", func(w io.Writer) error {
		tokens, err := apitoken.Open(cfg.DataDir).List()
		if err != nil {
			return err
		}
		for _, t := range tokens {
			// Names, scopes and dates only: the hashes stay in the data
			// directory.
			if _, err := fmt.Fprintf(w, "%s %s [%s] %s\n", t.ID, t.Name, t.Scopes, t.CreatedAt.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fail(err)
	}

	// The runtime file of a running core, if there is one.
	if data, err := os.ReadFile(cfg.RuntimePath()); err == nil {
		if err := add("runtime.json", func(w io.Writer) error {
			_, err := w.Write(data)
			return err
		}); err != nil {
			return fail(err)
		}
	}

	// The log files of the data directory.
	entries, err := os.ReadDir(cfg.LogDir())
	if err != nil {
		return fail(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := filepath.Join(cfg.LogDir(), entry.Name())
		if err := add("logs/"+entry.Name(), func(w io.Writer) error {
			return copyFileTo(w, name)
		}); err != nil {
			return fail(err)
		}
	}

	if err := zw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		return fail(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "wrote %s (%d bytes)\n", out, info.Size())
	return nil
}

// copyFileTo copies the file path to w.
func copyFileTo(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
