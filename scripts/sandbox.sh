#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# sandbox.sh runs a command with limits on processes, memory and CPU, so that
# a test that starts programs out of control (a fork bomb) or fills the memory
# cannot take the computer down. scripts/check.sh runs "go test" with it.
#
# On Linux with a systemd user session, the command runs in a transient scope
# (a cgroup). When the command ends, the scope ends too, with every program it
# left behind, also those that left their session or process group. Elsewhere,
# e.g. on macOS, on Windows or in a container without systemd, the command
# runs without limits, and the script says so.
#
# The graphics card needs no limit: the tests do not use it, and Linux has no
# cgroup controller for it. The low CPU and I/O weight keeps the desktop
# responsive while tests run.
#
# Limits, changeable by environment variable:
#   STREAMCREW_SANDBOX_TASKS   processes and threads together (default 2048)
#   STREAMCREW_SANDBOX_MEMORY  memory, without swap (default 50%, of the RAM)
#   STREAMCREW_SANDBOX_CPU     CPU time in percent of one core
#                              (default: half of all cores, e.g. 800% of 16)
#   STREAMCREW_SANDBOX=off     run the command without the sandbox
#
# Usage: scripts/sandbox.sh go test ./...
set -euo pipefail

if [[ $# -eq 0 ]]; then
	echo "usage: $0 command [args...]" >&2
	exit 2
fi

if [[ ${STREAMCREW_SANDBOX:-on} == off ]]; then
	exec "$@"
fi

if ! command -v systemd-run >/dev/null || ! systemd-run --user --scope --quiet true 2>/dev/null; then
	echo "sandbox: no systemd user session, running without limits" >&2
	exec "$@"
fi

cores=$(nproc 2>/dev/null || echo 2)
tasks=${STREAMCREW_SANDBOX_TASKS:-2048}
memory=${STREAMCREW_SANDBOX_MEMORY:-50%}
cpu=${STREAMCREW_SANDBOX_CPU:-$((cores * 50))%}
unit="streamcrew-sandbox-$$-${RANDOM}"

# stop ends the scope and every process still in it.
stop() {
	systemctl --user kill --signal=SIGKILL "${unit}.scope" 2>/dev/null || true
	systemctl --user stop "${unit}.scope" 2>/dev/null || true
}
trap stop EXIT

echo "sandbox: tasks=${tasks} memory=${memory} cpu=${cpu}" >&2
status=0
systemd-run --user --scope --quiet --collect --unit="${unit}" \
	-p TasksMax="${tasks}" \
	-p MemoryMax="${memory}" \
	-p MemorySwapMax=0 \
	-p CPUQuota="${cpu}" \
	-p CPUWeight=20 \
	-p IOWeight=20 \
	-- "$@" || status=$?
exit "${status}"
