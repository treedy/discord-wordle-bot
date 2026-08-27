# Multi-stage Dockerfile: build a static Go binary, run on small alpine image
FROM golang:1.27.0-alpine3.24 AS builder
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags='-s -w' -o /src/discord-wordle-bot ./

FROM alpine:3.24.1
RUN apk add --no-cache \
    ca-certificates \
    coreutils \
    tzdata
WORKDIR /app
COPY --from=builder /src/discord-wordle-bot /app/discord-wordle-bot
# COPY config.sample.json /app/config.json
RUN addgroup -g 1000 -S app && \
  adduser -u 1000 -S app -G app && \
  mkdir -p /app/db && \
  chown -R app:app /app
USER app
ENTRYPOINT ["/app/discord-wordle-bot"]
