FROM golang:1.25-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux \
    go build -trimpath -ldflags="-s -w" -o /out/autoscan-server ./cmd/autoscan-server

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc make libc6-dev ca-certificates valgrind \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /out/autoscan-server /usr/local/bin/autoscan-server

RUN mkdir -p /data

EXPOSE 8080

CMD ["autoscan-server"]
