#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# docker-smoke.sh builds the container image and checks it (roadmap phase 1.4):
#
#   1. the image runs as non-root
#   2. "version" and "config show" work, the configuration is server mode
#   3. "doctor" passes: /data is writable for the non-root user and the
#      embedded time zone database works in the scratch image
#   4. "serve" becomes healthy and ready (/healthz, /readyz)
#   5. docker stop (SIGTERM) ends it cleanly with exit code 0
#
# Usage: scripts/docker-smoke.sh [image]
#
# The core container runs without a network; the HTTP checks run in a
# container that joins its network namespace. This needs no port mapping.
# DOCKER_BUILD_ARGS passes extra arguments to docker build, e.g.
# "--network host" where the build containers have no working network.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

image=${1:-streamcrew:smoke}
name=streamcrew-smoke-$$
# The builder image of the Dockerfile has curl; reusing it avoids pulling and
# pinning another image.
probe_image=$(sed -n 's/^FROM \(golang:[^ ]*\) AS build$/\1/p' Dockerfile)

step() {
	printf '\n==> %s\n' "$*"
}

fail() {
	printf '\nsmoke test failed: %s\n' "$*" >&2
	if docker container inspect "$name" >/dev/null 2>&1; then
		printf '\ncontainer logs:\n' >&2
		docker logs "$name" >&2 || true
	fi
	exit 1
}

cleanup() {
	docker rm -f "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# probe fetches a path from the core inside its network namespace.
probe() {
	docker run --rm --network "container:$name" "$probe_image" \
		curl --silent --show-error --fail --max-time 2 "http://127.0.0.1:8740$1"
}

step "docker build"
read -ra build_args <<<"${DOCKER_BUILD_ARGS:-}"
docker build "${build_args[@]}" -t "$image" .

step "non-root user"
user=$(docker image inspect --format '{{.Config.User}}' "$image")
[[ $user == 65532:65532 ]] || fail "image runs as '$user', want 65532:65532"
echo "$user"

step "version"
docker run --rm --network none "$image" version

step "config show"
config=$(docker run --rm --network none "$image" config show)
echo "$config"
grep -qx 'mode: server' <<<"$config" || fail "config show: not in server mode"
grep -qx 'data_dir: /data' <<<"$config" || fail "config show: data_dir is not /data"

step "doctor"
docker run --rm --network none "$image" doctor || fail "doctor found problems"

step "serve"
docker run --detach --name "$name" --network none "$image" >/dev/null
for _ in $(seq 1 50); do
	if [[ $(probe /readyz 2>/dev/null) == ready ]]; then
		break
	fi
	if [[ $(docker container inspect --format '{{.State.Running}}' "$name") != true ]]; then
		fail "serve exited before it became ready"
	fi
	sleep 0.2
done
[[ $(probe /readyz) == ready ]] || fail "/readyz did not report ready"
[[ $(probe /healthz) == ok ]] || fail "/healthz did not report ok"
echo "healthy and ready"

step "docker stop (SIGTERM)"
docker stop --time 30 "$name" >/dev/null
code=$(docker container inspect --format '{{.State.ExitCode}}' "$name")
[[ $code == 0 ]] || fail "serve exited with code $code, want 0"
docker logs "$name" 2>&1 | grep -q '"msg":"streamcrew stopped"' || fail "no clean stop in the log"
echo "stopped cleanly"

printf '\nSmoke test passed: %s\n' "$image"
