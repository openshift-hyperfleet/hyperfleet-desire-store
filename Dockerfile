ARG BASE_IMAGE=registry.access.redhat.com/ubi9/ubi-minimal:latest

FROM registry.access.redhat.com/ubi9/go-toolset:9.8-1793736700 AS builder

ARG GIT_SHA=unknown
ARG GIT_DIRTY=""
ARG BUILD_DATE=""
ARG APP_VERSION="0.0.0-dev"

# Create /build as root and hand it to the non-root build user.
USER root
WORKDIR /build
RUN chown 1001:0 /build
USER 1001

COPY --chown=1001:0 go.mod go.sum ./
RUN --mount=type=cache,target=/opt/app-root/src/go/pkg/mod,uid=1001 \
    go mod download

COPY --chown=1001:0 . .

# Build the binary with FIPS mode on:
# - GOFIPS140=v1.0.0: use Go's FIPS-certified crypto module.
# - fips tag: include cmd/hyperfleet-desire-store/fips.go, which turns FIPS mode on.
# - no_openssl tag: use Go's own crypto; Red Hat Go's OpenSSL backend crashes on FIPS nodes with FIPS mode on.
RUN --mount=type=cache,target=/opt/app-root/src/go/pkg/mod,uid=1001 \
    --mount=type=cache,target=/opt/app-root/src/.cache/go-build,uid=1001 \
    GOOS=linux GOFIPS140=v1.0.0 \
    GIT_SHA=${GIT_SHA} GIT_DIRTY=${GIT_DIRTY} BUILD_DATE=${BUILD_DATE} APP_VERSION=${APP_VERSION} \
    make build GOFLAGS="-trimpath -tags=fips,no_openssl"

# Stop the build if the binary is not FIPS-ready. GOLANG_FIPS=1 makes Red Hat Go act as on a FIPS node.
RUN go version -m /build/bin/hyperfleet-desire-store | grep -Eq 'GOFIPS140=v1[.]0[.]0(-[[:xdigit:]]+)?$' || { echo "FIPS check FAILED: missing GOFIPS140=v1.0.0"; exit 1; }
RUN go version -m /build/bin/hyperfleet-desire-store \
    | grep -E '^[[:space:]]*build[[:space:]]+DefaultGODEBUG=([^,]*,)*fips140=on(,|$)' \
    | grep -Ev 'DefaultGODEBUG=([^,]*,)*fips140ems=0(,|$)' \
    || { echo "FIPS check FAILED: require fips140=on without fips140ems=0"; exit 1; }
RUN GOLANG_FIPS=1 /build/bin/hyperfleet-desire-store --help >/dev/null || { echo "FIPS check FAILED: binary does not start in FIPS mode"; exit 1; }

# Runtime stage
FROM ${BASE_IMAGE}

WORKDIR /app

COPY --from=builder /build/bin/hyperfleet-desire-store /app/hyperfleet-desire-store
COPY --from=builder /build/LICENSE /licenses/LICENSE

USER 65532:65532

# 8000 serves the Desire Store API data plane; 8080 serves /healthz and /readyz;
# 9090 serves /metrics.
EXPOSE 8000 8080 9090

ENTRYPOINT ["/app/hyperfleet-desire-store"]
CMD ["serve"]

ARG APP_VERSION="0.0.0-dev"
LABEL name="hyperfleet-desire-store" \
      vendor="Red Hat, Inc." \
      version="${APP_VERSION}" \
      summary="HyperFleet Desire Store API" \
      description="Desire Store API service that transports desires from HyperFleet adapters to appliers"
