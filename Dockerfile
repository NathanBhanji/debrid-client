# Runtime-only image: GoReleaser supplies the prebuilt static binary for the
# target platform. Static distroless provides CA certs, tzdata and a nonroot user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY debrid /usr/local/bin/debrid
ENV DEBRID_DATA_DIR=/data \
    DEBRID_DOWNLOAD_DIR=/downloads \
    DEBRID_SERVER__LISTEN=0.0.0.0:8080
VOLUME ["/data", "/downloads"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/debrid"]
CMD ["serve"]
