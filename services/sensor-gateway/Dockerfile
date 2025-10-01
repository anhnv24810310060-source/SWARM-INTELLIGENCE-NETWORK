# syntax=docker/dockerfile:1.7
FROM rust:1.81 as builder
WORKDIR /app
COPY ../../libs/rust/core ./libs/rust/core
COPY . .
# Build release
RUN cargo build --release || true

FROM gcr.io/distroless/cc-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/target/release/sensor-gateway /app/sensor-gateway
USER nonroot
ENV RUST_LOG=info
ENTRYPOINT ["/app/sensor-gateway"]
