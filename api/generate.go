// SPDX-License-Identifier: MIT

// Package api hosts the go:generate directive for the API contract
// (ADR-0010, Code-ADR-0016). The protobuf files in api/proto are the single
// source; buf generates the Go code into api/gen, which is checked in.
// The versions are pinned here and updated by Renovate, mirroring the sqlc
// convention (Code-ADR-0008).
package api

// The plugins are installed into GOPATH/bin, which buf finds on the PATH.
//go:generate go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
//go:generate go install connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.21.1
//go:generate go run github.com/bufbuild/buf/cmd/buf@v1.74.0 generate
