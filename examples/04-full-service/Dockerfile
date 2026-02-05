# Multi-stage build for Go service
FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /server ./examples/04-full-service

# Runtime image — non-root, minimal
FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app
COPY --from=builder /server /server

USER app

EXPOSE 8080 9090
HEALTHCHECK --interval=10s --timeout=3s CMD wget -qO- http://localhost:9090/health || exit 1

ENTRYPOINT ["/server"]
