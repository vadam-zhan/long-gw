# ================================================
# Stage 1: Builder
# ================================================
FROM golang:1.25-alpine AS builder

# Set Go proxy
ENV GOPROXY=https://goproxy.cn,direct

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

COPY . /build
RUN cd /build && go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o ./bin/gateway main.go

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

COPY --from=builder /build/bin/gateway .
COPY  --from=builder /build/etc/config.yaml /app/config.yaml

# Change ownership
RUN chown -R appuser:appgroup /app

USER appuser

# Default command
# ENTRYPOINT ["/app/gateway"]
# CMD ["gateway", "--config", "/app/config.yaml"]
#

CMD ["/app/gateway", "--config", "/app/config.yaml"]
