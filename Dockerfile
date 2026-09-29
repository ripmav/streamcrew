# SPDX-License-Identifier: Apache-2.0
#
# Container image of the streamcrew core (roadmap phase 1.4): a static,
# CGO-free binary in a scratch image, running as non-root. In the container
# the core runs in server mode (ADR-0003) with its data in the volume /data
# and JSON logs on stderr. Check it with scripts/docker-smoke.sh.
#
# The binary embeds the time zone database (time/tzdata); the image adds only
# the CA certificates for outgoing HTTPS connections.
#
# The build image is pinned by digest; Renovate updates it (Code-ADR-0001).

FROM golang:1.27.1-trixie@sha256:433790e515d27dc6003e847e644cc0af956985cf315c1c58a3b73ee2dd305183 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

# The whole context including .git, so that the binary carries the VCS
# revision as its version (internal/buildinfo).
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/streamcrew ./cmd/streamcrew \
	&& mkdir /out/data \
	&& mkdir -m 1777 /out/tmp

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/tmp /tmp
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /out/streamcrew /usr/local/bin/streamcrew

ENV STREAMCREW_MODE=server \
	STREAMCREW_DATA_DIR=/data \
	STREAMCREW_LOG_FORMAT=json \
	STREAMCREW_LOG_FILE=false

USER 65532:65532
VOLUME /data
EXPOSE 8740

ENTRYPOINT ["/usr/local/bin/streamcrew"]
CMD ["serve"]
