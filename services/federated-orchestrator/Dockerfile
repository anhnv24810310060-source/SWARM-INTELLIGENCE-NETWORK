# syntax=docker/dockerfile:1.7
FROM python:3.11-slim as runtime
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
COPY pyproject.toml .
RUN pip install --no-cache-dir fastapi uvicorn numpy protobuf
COPY . .
EXPOSE 8001
USER nobody
ENTRYPOINT ["python", "main.py"]
