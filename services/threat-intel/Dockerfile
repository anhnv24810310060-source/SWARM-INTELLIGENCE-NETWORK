# syntax=docker/dockerfile:1.7
FROM golang:1.22 as builder
WORKDIR /app
COPY . .
RUN go build -o threat-intel .
FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/threat-intel /app/threat-intel
USER nonroot
ENTRYPOINT ["/app/threat-intel"]
