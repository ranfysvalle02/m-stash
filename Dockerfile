# syntax=docker/dockerfile:1

# Build the browser assets once on the builder's native architecture. The output
# (frontend/dist) is static and architecture-independent, so BuildKit reuses it
# for every target platform instead of re-running Node under slow, crash-prone
# QEMU emulation.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-build

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# Compile the Go binary on the builder's native architecture and cross-compile
# for the requested target with the toolchain's own cross support (CGO off).
# This keeps every RUN step native, so multi-arch builds never touch QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-build /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w" -o /out/m-stash .

# The runtime image is the only target-architecture layer, and it is assembled
# with COPY alone (no RUN), so it requires zero emulation to produce.
FROM alpine:3.21

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/m-stash /usr/local/bin/m-stash

WORKDIR /app
ENV PORT=4000
EXPOSE 4000

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
	CMD wget -q -T 3 -O /dev/null http://127.0.0.1:${PORT}/readyz || exit 1

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/m-stash"]
