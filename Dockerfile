# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.23-alpine AS build

WORKDIR /src

# Cache module downloads in their own layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/poolwatch ./cmd/poolwatch

# ---- runtime stage ----
# distroless/static has no shell, so the binary probes itself via -healthcheck.
# Kubernetes can also probe GET /healthz (liveness) and GET /readyz (readiness).
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/poolwatch /app/poolwatch

EXPOSE 8080 6433

USER nonroot:nonroot

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/poolwatch", "-healthcheck"]

ENTRYPOINT ["/app/poolwatch"]
