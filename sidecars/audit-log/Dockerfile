# Stage 1: Builder
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /sidecar ./sidecars/audit-log/cmd/

# Stage 2: Final image
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /sidecar /sidecar
ENTRYPOINT ["/sidecar"]
