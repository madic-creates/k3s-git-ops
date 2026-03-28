FROM golang:1.26-alpine AS builder
RUN apk add --no-cache ca-certificates
WORKDIR /build
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /claude-alert-analyzer .

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /claude-alert-analyzer /claude-alert-analyzer
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/claude-alert-analyzer"]
