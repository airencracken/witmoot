FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /out/witmoot ./cmd/witmoot

FROM alpine:3.23
LABEL org.opencontainers.image.licenses="AGPL-3.0-or-later"
RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S -g 10001 witmoot \
	&& adduser -S -D -H -u 10001 -G witmoot -h /data witmoot \
	&& mkdir /data \
	&& chown witmoot:witmoot /data \
	&& chmod 0700 /data
COPY --from=build /out/witmoot /usr/local/bin/witmoot
COPY LICENSE README.md THIRD_PARTY.md /usr/share/doc/witmoot/
ENV WITMOOT_DATA_DIR=/data WITMOOT_ADDR=:8080 TMPDIR=/tmp
USER witmoot:witmoot
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
	CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/witmoot"]
