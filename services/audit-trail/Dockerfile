# syntax=docker/dockerfile:1.7
FROM golang:1.22 as builder
WORKDIR /app
COPY . .
RUN go build -o audit-trail .
FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/audit-trail /app/audit-trail
USER nonroot
ENTRYPOINT ["/app/audit-trail"]
