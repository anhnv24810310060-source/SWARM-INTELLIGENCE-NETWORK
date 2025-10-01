# syntax=docker/dockerfile:1.7
FROM python:3.11-slim as runtime
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
COPY pyproject.toml .
RUN pip install --no-cache-dir fastapi uvicorn minio protobuf
COPY . .
EXPOSE 8000
USER nobody
ENTRYPOINT ["python", "main.py"]
