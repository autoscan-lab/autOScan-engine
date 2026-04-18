FROM golang:1.22 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/autoscan-bridge ./cmd/autoscan-bridge

FROM python:3.12-slim-bookworm

ENV PYTHONUNBUFFERED=1

WORKDIR /app

RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc make \
    && rm -rf /var/lib/apt/lists/*

COPY service/requirements.txt ./service/requirements.txt
RUN pip install --no-cache-dir -r ./service/requirements.txt

COPY --from=builder /out/autoscan-bridge /usr/local/bin/autoscan-bridge
COPY service ./service

RUN mkdir -p /data

EXPOSE 8080

CMD ["uvicorn", "service.main:app", "--host", "0.0.0.0", "--port", "8080"]
