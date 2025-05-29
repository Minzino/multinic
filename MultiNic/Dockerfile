# Build the manager binary
FROM golang:1.24-alpine AS builder

# Install git and ca-certificates for security
RUN apk add --no-cache git ca-certificates

ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

# Build with optimized flags
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -a -installsuffix cgo \
    -ldflags='-w -s -extldflags "-static"' \
    -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy the manager binary
COPY --from=builder /workspace/manager .

# Use non-root user for security
USER 65532:65532

# Add labels for better metadata
LABEL maintainer="multinic-team"
LABEL version="1.0"
LABEL description="MultiNic Kubernetes Controller"

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/manager", "--health-check"]

ENTRYPOINT ["/manager"]
