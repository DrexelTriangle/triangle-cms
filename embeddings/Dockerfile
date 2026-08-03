FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    # fastembed caches downloaded ONNX weights here. Baking them into the image
    # keeps startup off the network: a container that has to fetch ~130MB before
    # it can answer is a container that fails its healthcheck on a bad day.
    FASTEMBED_CACHE_PATH=/opt/fastembed

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py .

ARG EMBED_MODEL=BAAI/bge-small-en-v1.5
ENV EMBED_MODEL=${EMBED_MODEL}
RUN python -c "from fastembed import TextEmbedding; import os; TextEmbedding(model_name=os.environ['EMBED_MODEL'])"

RUN useradd --create-home --uid 10001 embed && chown -R embed:embed /opt/fastembed
USER embed

EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
