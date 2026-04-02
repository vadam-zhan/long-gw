# ================================================
# Stage 1: Builder
# ================================================
FROM golang:1.25-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

COPY . /build
RUN cd /build && go mod tidy && go build -ldflags="-w -s" -o ./bin/long-gw main.go

# ================================================
# Stage 2: Final image
# ================================================
FROM alpine:3.19 AS final

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 appgroup && \
    adduser -u 1000 -G appgroup -s /bin/sh -D appuser

WORKDIR /app

COPY --from=builder /build/bin/long-gw .
COPY  --from=builder /build/etc/config.yaml /app/config.yaml

# Change ownership
RUN chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose ports
EXPOSE 8080 8081

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/v1/admin/health || exit 1

# Default command
ENTRYPOINT ["/app/long-gw"]
CMD ["gateway", "--config", "/app/config.yaml"]
