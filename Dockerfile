# ---- Build stage ----
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Cache dependencies
ENV GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

# Build binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/agentmate ./cmd/server

# ---- Runtime stage ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/agentmate .
COPY --from=builder /app/web ./web

EXPOSE 26001

ENTRYPOINT ["/app/agentmate"]
