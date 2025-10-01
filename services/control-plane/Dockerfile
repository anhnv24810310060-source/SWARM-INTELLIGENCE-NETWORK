# syntax=docker/dockerfile:1.7
FROM golang:1.22 as builder
WORKDIR /app
COPY . .
RUN go build -o control-plane .
FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/control-plane /app/control-plane
USER nonroot
ENTRYPOINT ["/app/control-plane"]
