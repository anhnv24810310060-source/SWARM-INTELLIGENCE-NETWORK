# syntax=docker/dockerfile:1.7
FROM golang:1.22 as builder
WORKDIR /app
COPY . .
RUN go build -o billing-service .
FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/billing-service /app/billing-service
USER nonroot
ENTRYPOINT ["/app/billing-service"]
