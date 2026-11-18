# syntax=docker/dockerfile:1
# sbpanel runtime image. It holds no sbpanel binaries: on start the entrypoint downloads the
# release given by SBP_VERSION (default: the latest), verifies it against checksums.txt and
# caches it in the data volume. Restarting the container picks up a newer release.
#   docker run -d -p 127.0.0.1:8080:8080 -p 9443:9443 -v sbmaster:/var/lib/sbmaster ghcr.io/genguzzz/sbpanel
#   docker run -d --network host -v sbnode:/var/lib/sbnode \
#     -e SBN_MASTER=... -e SBN_TOKEN=... -e SBN_CA_SHA256=... ghcr.io/genguzzz/sbpanel node
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata curl tar
COPY deploy/docker/entrypoint.sh /usr/local/bin/sbpanel-entrypoint
# SBP_CONTAINER: the panel's binary swap doesn't apply; upgrades come from a container restart
ENV SBP_CONTAINER=1 SBP_VERSION=latest SBP_MIRROR= \
    SBM_DATA=/var/lib/sbmaster SBM_WEB=:8080 SBM_GRPC=:9443 \
    SBN_DIR=/var/lib/sbnode \
    PATH=/var/lib/sbmaster/bin:/var/lib/sbnode/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
EXPOSE 8080 9443
VOLUME ["/var/lib/sbmaster", "/var/lib/sbnode"]
LABEL org.opencontainers.image.source="https://github.com/genguzzz/sbpanel" \
      org.opencontainers.image.description="sbpanel runtime: downloads and runs sbmaster or sbnode" \
      org.opencontainers.image.licenses="GPL-3.0"
ENTRYPOINT ["sbpanel-entrypoint"]
CMD ["master"]
