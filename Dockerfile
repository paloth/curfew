# Base images are pinned by digest (multi-arch index) so a re-pointed tag cannot change the build.
# Refresh: docker buildx imagetools inspect <image:tag>, and copy the top "Digest:".

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /workspace

# Separate step so the dependency layer stays cached until go.mod/go.sum change
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for a static binary; -w -s strip debug info
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build \
    -ldflags="-w -s -X curfew/cmd.Version=${VERSION}" \
    -o curfew .

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

COPY --from=builder /workspace/curfew /usr/local/bin/curfew

# Numeric form of distroless "nonroot": runAsNonRoot can only verify a numeric UID
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/curfew"]
