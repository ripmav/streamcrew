// SPDX-License-Identifier: MIT

// Package doctor checks the environment of the core for "streamcrew doctor"
// (ADR-0011): data directory, configuration file, listen address and time
// zone database. Later phases add checks for tokens and connections.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Status is the outcome of a check.
type Status string

// Outcomes of a check.
const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// probeTimeout limits the request to a core that already listens on the
// address.
const probeTimeout = 2 * time.Second

// Result is the outcome of one check.
type Result struct {
	Check  string `json:"check"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
}

// Config names what to check.
type Config struct {
	// DataDir is the data directory.
	DataDir string
	// ConfigFile is the configuration file that was read, or "" if none.
	ConfigFile string
	// Listen is the listen address of the HTTP server.
	Listen string
}

// Run runs all checks.
func Run(ctx context.Context, cfg Config) []Result {
	return []Result{
		checkDataDir(cfg.DataDir),
		checkConfigFile(cfg.ConfigFile),
		checkListen(ctx, cfg.Listen),
		checkTimeZones(),
	}
}

// Failed reports whether any result has StatusFail.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == StatusFail {
			return true
		}
	}
	return false
}

func checkDataDir(dir string) Result {
	const name = "data directory"
	info, err := os.Stat(dir)
	switch {
	case err == nil && !info.IsDir():
		return Result{name, StatusFail, dir + " is not a directory"}
	case err == nil:
		if err := probeWritable(dir); err != nil {
			return Result{name, StatusFail, fmt.Sprintf("%s is not writable: %v", dir, err)}
		}
		return Result{name, StatusOK, dir}
	case !errors.Is(err, fs.ErrNotExist):
		return Result{name, StatusFail, err.Error()}
	}

	// The directory is created on the first start; check that this will work.
	parent := filepath.Dir(dir)
	for {
		info, err := os.Stat(parent)
		if err == nil && info.IsDir() {
			break
		}
		next := filepath.Dir(parent)
		if next == parent {
			return Result{name, StatusFail, "no existing parent directory for " + dir}
		}
		parent = next
	}
	if err := probeWritable(parent); err != nil {
		return Result{name, StatusFail, fmt.Sprintf("%s does not exist and %s is not writable: %v", dir, parent, err)}
	}
	return Result{name, StatusOK, dir + " (created on the first start)"}
}

func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".streamcrew-doctor-*")
	if err != nil {
		return err
	}
	return errors.Join(f.Close(), os.Remove(f.Name()))
}

func checkConfigFile(path string) Result {
	const name = "config file"
	if path == "" {
		return Result{name, StatusOK, "none; defaults, environment variables and flags only"}
	}
	return Result{name, StatusOK, path}
}

func checkListen(ctx context.Context, addr string) Result {
	const name = "listen address"
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err == nil {
		if err := ln.Close(); err != nil {
			return Result{name, StatusWarn, fmt.Sprintf("%s is available, but closing the probe failed: %v", addr, err)}
		}
		return Result{name, StatusOK, addr + " is available"}
	}
	if probeCore(ctx, addr) {
		return Result{name, StatusOK, addr + " is used by a running streamcrew core"}
	}
	return Result{name, StatusFail, fmt.Sprintf("%s cannot be used: %v", addr, err)}
}

// probeCore reports whether a streamcrew core answers /healthz on addr.
func probeCore(ctx context.Context, addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func checkTimeZones() Result {
	const name = "time zone database"
	if _, err := time.LoadLocation("Europe/Berlin"); err != nil {
		return Result{name, StatusFail, err.Error()}
	}
	return Result{name, StatusOK, "available"}
}
