# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG BUILD_DATE=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.buildDate=${BUILD_DATE}" -o /out/nebenkostenrechner ./cmd/nebenkostenrechner

# /data-prep is an empty directory pre-owned by distroless' built-in
# nonroot user (65532:65532) - distroless has no shell/mkdir/chown, so this
# has to happen here in the builder stage. Docker initializes a brand-new
# named volume from whatever's already at its mount point in the image, so
# a fresh /data volume inherits this ownership on first run; an existing
# (root-owned) volume from before this change still needs a one-time manual
# `chown -R 65532:65532` - see README.
RUN mkdir -p /data-prep && chown 65532:65532 /data-prep

FROM gcr.io/distroless/static-debian12
COPY --from=builder --chown=65532:65532 /data-prep /data
WORKDIR /data
ENV DB_PATH=/data/nebenkosten.db
COPY --from=builder /out/nebenkostenrechner /nebenkostenrechner
USER nonroot:nonroot
ENTRYPOINT ["/nebenkostenrechner"]
