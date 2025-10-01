# syntax=docker/dockerfile:1.7
FROM rust:1.81 as builder
WORKDIR /app
COPY ../../libs/rust/core ./libs/rust/core
COPY . .
RUN cargo build --release || true
FROM gcr.io/distroless/cc-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/target/release/inference-gateway /app/inference-gateway
USER nonroot
ENV RUST_LOG=info
ENTRYPOINT ["/app/inference-gateway"]
