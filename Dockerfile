FROM golang:1.24-alpine as builder

WORKDIR /app

# Copy go.mod first
COPY go.mod ./

# Copy the rest of the code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o canvas-server

# Use a smaller image for the final container
FROM alpine:latest

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/canvas-server .
# Copy static files
COPY static/ ./static/

# Expose the port
EXPOSE 8080

# Run the binary
CMD ["./canvas-server"]