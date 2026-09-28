FROM node:22-alpine AS frontend-build

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-build /src/frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/m-stash .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=build /out/m-stash /usr/local/bin/m-stash

WORKDIR /app
ENV PORT=4000
ENV M_STASH_CONFIG=/etc/m-stash/gateway.json
EXPOSE 4000

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
	CMD wget -q -T 3 -O /dev/null http://127.0.0.1:${PORT}/readyz || exit 1

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/m-stash"]