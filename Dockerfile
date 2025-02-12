### BUILD STAGE ###
FROM --platform=$BUILDPLATFORM golang:1.23 AS builder

ARG CGO_ENABLED=0
ARG VERSION=1.0.0
ARG BUILD_TAGS=release
ARG GOOS
ARG GOARCH
ARG BIN

RUN GOOS=${GOOS:-$(go env GOOS)} && \
  GOARCH=${GOARCH:-$(go env GOARCH)} 

# Set environment variables
ENV CGO_ENABLED=$CGO_ENABLED \
  GOOS=$GOOS \
  GOARCH=$GOARCH

WORKDIR /app
COPY go.mod .
COPY go.sum .

# Download dependencies
RUN go mod tidy
RUN go mod download

COPY . .

# Build the Go application with optimizations
# RUN go build -tags="$BUILD_TAGS" -ldflags "-X main.Version=$VERSION -s -w" -o app "./cmd/$BIN"
RUN go build -o app "./cmd/$BIN"

# === RUN STAGE ===
FROM --platform=$BUILDPLATFORM gcr.io/distroless/static:nonroot

WORKDIR /app

# Copy built binary from builder stage
COPY --from=builder /app/app .

# Run the application
CMD ["./app"]
