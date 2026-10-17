// SPDX-License-Identifier: MIT

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func consoleCfg(t *testing.T, b *bytes.Buffer) Config {
	t.Helper()
	return Config{Console: b, ConsoleFormat: FormatText, Level: slog.LevelInfo}
}

// TestDynamicLevelChangeApplies verifies a level change reaches every
// derived component logger without rebuilding them (roadmap 6.6).
func TestDynamicLevelChangeApplies(t *testing.T) {
	var buf bytes.Buffer
	logger, dyn, err := NewDynamic(consoleCfg(t, &buf))
	if err != nil {
		t.Fatal(err)
	}
	store := logger.With(ComponentKey, "store")
	engine := logger.With(ComponentKey, "engine")

	store.Debug("store debug at info level")
	if buf.Len() != 0 {
		t.Fatalf("debug at info level: %q", buf.String())
	}

	// The base level goes to debug and the engine gets its own level.
	cfg := consoleCfg(t, &buf)
	cfg.Level = slog.LevelDebug
	cfg.ComponentLevels = map[string]slog.Level{"engine": slog.LevelDebug}
	if err := dyn.Update(cfg); err != nil {
		t.Fatal(err)
	}
	engine.Debug("engine debug after the update")
	store.Debug("store debug after the update")
	if !strings.Contains(buf.String(), "engine debug") || !strings.Contains(buf.String(), "store debug") {
		t.Fatalf("level change not applied: %q", buf.String())
	}

	// Back to info: the base level applies again, the override stays.
	cfg.Level = slog.LevelInfo
	if err := dyn.Update(cfg); err != nil {
		t.Fatal(err)
	}
	store.Debug("store debug back at info")
	engine.Debug("engine debug still allowed")
	if strings.Contains(buf.String(), "store debug back at info") {
		t.Fatalf("base level change not applied: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "engine debug still allowed") {
		t.Fatalf("component override lost: %q", buf.String())
	}
}

// TestDynamicOutputChangeApplies verifies a console format change switches
// the output of the whole logger tree (roadmap 6.6).
func TestDynamicOutputChangeApplies(t *testing.T) {
	var buf bytes.Buffer
	logger, dyn, err := NewDynamic(consoleCfg(t, &buf))
	if err != nil {
		t.Fatal(err)
	}
	log := logger.With(ComponentKey, "x")
	log.Info("before")
	if !strings.Contains(buf.String(), "msg=before") {
		t.Fatalf("text format: %q", buf.String())
	}
	buf.Reset()

	cfg := consoleCfg(t, &buf)
	cfg.ConsoleFormat = FormatJSON
	if err := dyn.Update(cfg); err != nil {
		t.Fatal(err)
	}
	log.Info("after")
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("json format: %q: %v", buf.String(), err)
	}
	if entry["msg"] != "after" || entry["component"] != "x" {
		t.Fatalf("json entry: %v", entry)
	}
}

// TestDynamicFileSwitch verifies a rotation change rebuilds the log file
// and the new file is written (roadmap 6.6).
func TestDynamicFileSwitch(t *testing.T) {
	file := t.TempDir() + "/app.log"
	var buf bytes.Buffer
	cfg := consoleCfg(t, &buf)
	cfg.File = file
	cfg.MaxSize = 10 << 20
	cfg.MaxFiles = 5
	logger, dyn, err := NewDynamic(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("first")

	cfg.MaxSize = 1 << 20
	if err := dyn.Update(cfg); err != nil {
		t.Fatal(err)
	}
	logger.Info("second")
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "second") {
		t.Fatalf("new file not written: %q", data)
	}
}
