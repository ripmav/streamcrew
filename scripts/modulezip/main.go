// SPDX-License-Identifier: MIT

// Command modulezip creates the module source archive of streamcrew in the
// layout of a Go module proxy (ADR-0025): a zip whose dirhash is the h1:
// value of go.sum. It is run from the repository root:
//
//	go run ./scripts/modulezip -version v1.2.3 -out dist/streamcrew-v1.2.3-src.zip
//
// The archive contains every file of the module (without .git) with fixed
// timestamps, so it is reproducible for the same tree. The program verifies
// the archive with x/mod/zip.CheckZip before it exits.
package main

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/sumdb/dirhash"
	zipmod "golang.org/x/mod/zip"
)

// modulePath is the import path of the archive.
const modulePath = "github.com/ripmav/streamcrew"

// zeroTime is the fixed file time of every archive entry.
//
//nolint:gochecknoglobals // constant table value
var zeroTime = time.Time{}

func main() {
	log.SetFlags(0)
	log.SetPrefix("modulezip: ")

	version := flag.String("version", "", "the module version, e.g. v1.2.3")
	out := flag.String("out", "", "the output file")
	flag.Parse()
	if *version == "" || *out == "" {
		flag.Usage()
		os.Exit(2)
	}

	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if git, err := exec.CommandContext(context.Background(), "git", "rev-parse", "--show-toplevel").Output(); err == nil {
		root = strings.TrimSpace(string(git))
	}
	prefix := modulePath + "@" + *version

	// collect the files, sorted by archive name
	var files []string
	err = filepath.Walk(root, func(path string, d os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .git keeps no version, so it stays out of the archive; a
			// nested go.mod starts a separate module and must stay out,
			// too (the modulecheck harness is not part of the module)
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			if rel, err := filepath.Rel(root, path); err == nil && rel != "." {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Mode()&os.ModeSymlink != 0 {
			return nil // symlinks do not survive a module zip
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == filepath.ToSlash(*out) {
			return nil // the archive must not contain itself
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	sort.Slice(files, func(i, j int) bool {
		ri, _ := filepath.Rel(root, files[i])
		rj, _ := filepath.Rel(root, files[j])
		return filepath.ToSlash(ri) < filepath.ToSlash(rj)
	})

	// read the files and hash them for the h1: dirhash
	blobs := make(map[string][]byte, len(files))
	var names []string
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		blobs[filepath.ToSlash(rel)] = data
		names = append(names, prefix+"/"+filepath.ToSlash(rel))
	}
	open := func(name string) (io.ReadCloser, error) {
		rel := strings.TrimPrefix(name, prefix+"/")
		return io.NopCloser(bytes.NewReader(blobs[rel])), nil
	}
	h1, err := dirhash.Hash1(names, open)
	if err != nil {
		log.Fatal(err)
	}

	// write the archive with fixed timestamps
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		hdr := &zip.FileHeader{Name: prefix + "/" + filepath.ToSlash(rel), Method: zip.Deflate}
		hdr.Modified = zeroTime
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := w.Write(blobs[filepath.ToSlash(rel)]); err != nil {
			log.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	// verify the archive against the module zip rules
	checked, err := zipmod.CheckZip(module.Version{Path: modulePath, Version: *version}, *out)
	if err != nil {
		log.Fatal(err)
	}
	if err := checked.Err(); err != nil {
		log.Fatal(err)
	}
	fmt.Fprint(os.Stdout, *out, " ", h1, "\n")
}
