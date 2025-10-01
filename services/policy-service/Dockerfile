# syntax=docker/dockerfile:1.7
FROM golang:1.22 as builder
WORKDIR /app
COPY . .
RUN go build -o policy-service .

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/policy-service /app/policy-service
USER nonroot
ENV GIN_MODE=release
ENTRYPOINT ["/app/policy-service"]
