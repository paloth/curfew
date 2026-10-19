# Base images are pinned by digest (multi-arch index) so a re-pointed tag cannot change the build.
# Refresh: docker buildx imagetools inspect <image:tag>, and copy the top "Digest:".

# Build stage
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
ARG TARGETARCH
ARG VERSION=dev

# Set working directory
WORKDIR /workspace

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies (cached if go.mod/go.sum haven't changed)
RUN go mod download

# Copy source code
COPY . .

# Build the binary
# CGO_ENABLED=0 for static binary
# -ldflags="-w -s" to strip debug info and reduce size
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build \
    -ldflags="-w -s -X curfew/cmd.Version=${VERSION}" \
    -o curfew .

# Final stage - use distroless for minimal attack surface (ships CA certificates)
FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

# Copy binary from builder
COPY --from=builder /workspace/curfew /usr/local/bin/curfew

# Numeric non-root user (distroless "nonroot"): runAsNonRoot can only verify a numeric UID
USER 65532:65532

# Set entrypoint
ENTRYPOINT ["/usr/local/bin/curfew"]
