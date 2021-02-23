FROM golang:1.14 AS builder

# Set necessary environmet variables needed for our image
ENV CGO_ENABLED=1 \
    GOOS=linux \
    GOARCH=arm64

# Move to working directory /build
WORKDIR /build

# Copy the code into the container
COPY . .

# Build the application
RUN go build -v -a -tags netgo -ldflags '-w -extldflags "-static"' .

# Move to /dist directory as the place for resulting binary folder
WORKDIR /dist

# Copy binary from build to main folder
RUN cp /build/github2telegram .

# Build a small image
FROM scratch

WORKDIR /app

COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.pem
COPY --from=builder /dist/github2telegram /app

# Command to run
ENTRYPOINT ["/app/github2telegram"]
