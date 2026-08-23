# Runtime-only image: GoReleaser supplies the prebuilt static binary for each
# platform under <os>/<arch>/ in the build context. Static distroless provides
# CA certs, tzdata and a nonroot user (uid 65532).

# The data/download directories must be owned by the nonroot user so the
# container starts with a fresh named volume; distroless has no chown, so
# create them in a helper stage and copy them in.
FROM busybox:1.36 AS dirs
RUN mkdir -p /data /downloads && chown -R 65532:65532 /data /downloads

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/debrid /usr/local/bin/debrid
COPY --from=dirs --chown=65532:65532 /data /data
COPY --from=dirs --chown=65532:65532 /downloads /downloads
ENV DEBRID_DATA_DIR=/data \
    DEBRID_DOWNLOAD_DIR=/downloads \
    DEBRID_SERVER__LISTEN=0.0.0.0:8080
VOLUME ["/data", "/downloads"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/debrid"]
CMD ["serve"]
