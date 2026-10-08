# SPDX-License-Identifier: Apache-2.0
#
# The whole service in one static binary on a distroless base.
#
#   docker build -t aicc-knowledge:dev .
#   docker build --build-arg VERSION=v0.1.0 -t aicc-knowledge:v0.1.0 .
#
# BASE_IMAGE names the runtime layer and GOPROXY the Go module proxy, for hosts
# that cannot reach the defaults.
ARG BASE_IMAGE=gcr.io/distroless/static-debian12:nonroot

# The build stage runs on the builder's own architecture; only its output is
# per-target. Go cross-compiles with CGO off, so a multi-arch build emulates
# nothing.
FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine AS build

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=""
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/aicc-knowledge ./cmd/aicc-knowledge

# --- what ships ---------------------------------------------------------------
FROM ${BASE_IMAGE}

COPY --from=build /out/aicc-knowledge /usr/local/bin/aicc-knowledge
# The license, notices and third-party license texts for what is in the binary.
COPY LICENSE NOTICE THIRD_PARTY_LICENSES /licenses/

ENV KB_HTTP_ADDR=:8080 \
    KB_OPS_ADDR=:9090

# 8080 is the authenticated API. 9090 carries /metrics, /healthz and /readyz and
# has no authentication: publish it only on a private network.
EXPOSE 8080 9090

USER nonroot
ENTRYPOINT ["/usr/local/bin/aicc-knowledge"]
CMD ["serve"]
