# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -m 1777 /runtime-tmp

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOMAXPROCS=2 \
    go build -p=1 -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /out/eink-server ./cmd/eink-server

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /out/eink-server /eink-server
COPY --from=build /runtime-tmp /tmp

WORKDIR /
VOLUME ["/data"]
EXPOSE 8080 11113

ENTRYPOINT ["/eink-server"]
CMD ["--config", "/config/eink-server.toml"]
