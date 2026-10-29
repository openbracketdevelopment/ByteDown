FROM golang:1.25.14-trixie@sha256:2c4c60ef415fbfa5e90300722293bef36c5e63fae17570ce18f580af933dbd73 AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libvips-dev liblcms2-dev pkg-config libheif-plugin-aomenc libheif-plugin-aomdec \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /out/bytedown ./cmd/bytedown
RUN mkdir -p /out/licenses \
    && go list -m -f '{{if not .Main}}{{.Path}} {{.Dir}}{{end}}' all \
    | while read -r module directory; do \
        [ -n "$directory" ] || continue; \
        mkdir -p "/out/licenses/$module"; \
        for notice in "$directory"/LICENSE* "$directory"/COPYING* "$directory"/NOTICE*; do \
            if [ -f "$notice" ]; then cp "$notice" "/out/licenses/$module/"; fi; \
        done; \
    done

FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates libvips42t64 libheif-plugin-aomenc libheif-plugin-aomdec \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 bytedown \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin bytedown \
    && install -d -o 10001 -g 10001 -m 0700 /data
COPY --from=build /out/bytedown /usr/local/bin/bytedown
COPY --from=build /usr/local/go/LICENSE /usr/share/doc/bytedown/GO-LICENSE
COPY --from=build /out/licenses /usr/share/doc/bytedown/modules
COPY LICENSE docs/third-party-licenses.md /usr/share/doc/bytedown/
ENV STORAGE_DIR=/data LISTEN_ADDR=:8080 VIPS_CONCURRENCY=1
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=30s --timeout=10s --start-period=20s --retries=3 \
    CMD ["/usr/local/bin/bytedown", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/bytedown"]
CMD ["api"]
