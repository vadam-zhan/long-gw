# Build stage
FROM golang:1.25-alpine AS builder

# Set Go proxy
ENV GOPROXY=https://goproxy.cn,direct

# Install build dependencies
RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY . .
RUN go mod tidy

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /app/auth .

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Set timezone
ENV TZ=Asia/Shanghai

# Create non-root user
RUN addgroup -g 1000 appgroup && \
    adduser -u 1000 -G appgroup -s /bin/sh -D appuser

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/auth .

# Copy default config
COPY --from=builder /app/etc/config.yaml /app/config.yaml

# Change ownership
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Run the binary
CMD ["/app/auth", "--f", "/app/config.yaml"]
