FROM golang:1.24-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/m-stash .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates
COPY --from=build /out/m-stash /usr/local/bin/m-stash

ENV PORT=4000
EXPOSE 4000

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/m-stash"]