# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download

# Copy source
COPY gateway/ .

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o gateway .

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/gateway .
COPY config/gateway.json ./config/gateway.json

EXPOSE 8080 9090

CMD ["./gateway"]
