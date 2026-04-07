# Build stage
FROM golang:1.25-alpine AS builder

ARG APPLICATION=github-runner-exporter
ARG VERSION=dev
ARG REVISION=local
ARG BUILD_RFC3339=1970-01-01T00:00:00Z

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  go build \
  -ldflags "-s -w \
  -X main.version=${VERSION} \
  -X main.revision=${REVISION} \
  -X main.buildRFC3339=${BUILD_RFC3339}" \
  -o /bin/${APPLICATION} \
  ./cmd/${APPLICATION}/main.go

# Final stage — scratch for minimal attack surface
FROM scratch

ARG APPLICATION=github-runner-exporter
COPY --from=builder /bin/${APPLICATION} /bin/${APPLICATION}

# Run as non-root (UID 10001)
USER 10001

EXPOSE 9102

ENTRYPOINT ["/bin/github-runner-exporter"]
