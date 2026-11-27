FROM python:3.12-slim
WORKDIR /app
RUN pip install --no-cache-dir asyncpg==0.30.0 httpx==0.27.2
COPY webhook_dispatcher.py /app/webhook_dispatcher.py
CMD ["python", "-u", "/app/webhook_dispatcher.py"]
