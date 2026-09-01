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

FROM gcr.io/distroless/static-debian12
WORKDIR /data
ENV DB_PATH=/data/nebenkosten.db
COPY --from=builder /out/nebenkostenrechner /nebenkostenrechner
ENTRYPOINT ["/nebenkostenrechner"]
