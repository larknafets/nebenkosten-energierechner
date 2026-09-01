# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags="-s -w" -o /out/nebenkosten-energierechner ./cmd/nebenkosten-energierechner

FROM gcr.io/distroless/static-debian12
WORKDIR /data
ENV DB_PATH=/data/nebenkosten.db
COPY --from=builder /out/nebenkosten-energierechner /nebenkosten-energierechner
ENTRYPOINT ["/nebenkosten-energierechner"]
