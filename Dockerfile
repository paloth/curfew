# Base images are pinned by digest (multi-arch index) so a re-pointed tag cannot change the build.
# Refresh: docker buildx imagetools inspect <image:tag>, and copy the top "Digest:".

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:516757106de35205efb70b91ba03f6f1298424a0678c603255cf2852a7000087

COPY --from=builder /workspace/curfew /usr/local/bin/curfew

# Numeric form of distroless "nonroot": runAsNonRoot can only verify a numeric UID
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/curfew"]
