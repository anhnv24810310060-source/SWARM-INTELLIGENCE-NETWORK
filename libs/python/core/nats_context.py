"""Trace context helpers for NATS message publishing / subscription (Python stub).

Goal: Mirror capabilities of Go `natsctx` package by injecting / extracting W3C
traceparent headers once Python services begin publishing / consuming NATS
messages. This is intentionally dependency-light (no direct NATS client import)
so services can adopt it incrementally.

Usage (future):

    from . import nats_context
    nats_context.inject(headers, context=get_current_span().get_span_context())
    # publish with headers dict using chosen NATS Python client

For now we lean on OpenTelemetry propagators so once OTEL is initialized via
`tracing_util.init_tracing(service)` these functions will interoperate with any
other instrumented language.
"""

from __future__ import annotations

from typing import Dict, Optional
from opentelemetry import context as otel_context
from opentelemetry.propagate import inject as otel_inject, extract as otel_extract

TRACEPARENT_KEY = "traceparent"


def inject(headers: Dict[str, str], carrier_context: Optional[otel_context.Context] = None) -> None:
    """Inject current trace context into the provided headers dict.

    headers: mutable mapping that will be updated in-place.
    carrier_context: explicit OTEL context; if omitted, uses current.
    """

    if carrier_context is None:
        carrier_context = otel_context.get_current()

    # opentelemetry propagator works with a setter (headers must act like dict)
    def setter(carrier, key, value):  # pragma: no cover - trivial
        carrier[key] = value

    otel_inject(headers, context=carrier_context, setter=setter)


def extract(headers: Dict[str, str]):
    """Extract an OTEL context from headers (returns OTEL Context object).

    If no traceparent present, returns a context suitable for starting a new root span.
    """
    def getter(carrier, key):  # pragma: no cover - trivial
        val = carrier.get(key)
        if val is None:
            return []
        return [val]

    return otel_extract(headers, getter=getter)


__all__ = ["inject", "extract", "TRACEPARENT_KEY"]
