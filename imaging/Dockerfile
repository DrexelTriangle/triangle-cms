FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py .

# Fail the build, not the first render, if the bundled libvips cannot write
# WebP.
RUN python -c "import pyvips; pyvips.Image.black(8, 8).webpsave_buffer()"

RUN useradd --create-home --uid 10001 imaging
USER imaging

EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
