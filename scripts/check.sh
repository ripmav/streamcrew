#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# check.sh runs the pre-commit checklist (docs/plan.md §11.1, Code-ADR-0001)
# in the required order. Run it before every commit.
#
# go fix and gofmt rewrite files. They run first so that the checks after them
# see the final code; review the resulting diff before committing.
#
# golangci-lint is taken from $GOLANGCI_LINT, then from PATH, then from
# ~/.local/bin.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

golangci_lint=${GOLANGCI_LINT:-}
if [[ -z $golangci_lint ]]; then
	if command -v golangci-lint >/dev/null; then
		golangci_lint=golangci-lint
	elif [[ -x $HOME/.local/bin/golangci-lint ]]; then
		golangci_lint=$HOME/.local/bin/golangci-lint
	else
		echo "golangci-lint not found; see docs/adr/code/0001-go-toolchain-und-linting.md" >&2
		exit 1
	fi
fi

gobin=$(go env GOBIN)
gobin=${gobin:-$(go env GOPATH)/bin}

step() {
	printf '\n==> %s\n' "$*"
	"$@"
}

# go_checksums prints a checksum per Go file, to detect rewrites by go fix and
# gofmt independently of other uncommitted changes.
go_checksums() {
	find . -name '*.go' -not -path './.git/*' -print0 | sort -z | xargs -0 -r sha256sum
}

before=$(go_checksums)
step go fix ./...
step gofmt -w .
after=$(go_checksums)
if [[ $before != "$after" ]]; then
	printf '\ngo fix/gofmt rewrote these files; review the diff before committing:\n'
	diff <(printf '%s\n' "$before") <(printf '%s\n' "$after") | awk '/^>/ { print "  " $3 }' || true
fi
step go vet ./...
step "$golangci_lint" run ./...
step go install golang.org/x/vuln/cmd/govulncheck@latest
step "$gobin/govulncheck" ./...
step go test ./...

printf '\nAll checks passed.\n'
