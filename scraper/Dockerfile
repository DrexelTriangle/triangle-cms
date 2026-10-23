FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Camoufox is a Firefox build, so Firefox's shared-library list is the right
# one. Needs root for apt, hence before the user switch.
RUN python -m playwright install-deps firefox

RUN useradd --create-home --uid 10001 scraper

# Fetched at build time so a cold container never reaches out for the
# browser. The fetch also writes a fingerprint model into site-packages, which
# only root can do, so it runs as root with the browser cache pointed at the
# scraper user's home and the files handed over afterwards.
RUN HOME=/home/scraper python -m camoufox fetch && chown -R scraper:scraper /home/scraper

USER scraper

COPY app.py .

EXPOSE 8000
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8000", "--workers", "1"]
