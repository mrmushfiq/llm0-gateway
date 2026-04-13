FROM python:3.12-slim

WORKDIR /app

# curl is needed for the Docker healthcheck
RUN apt-get update && apt-get install -y --no-install-recommends curl && rm -rf /var/lib/apt/lists/*

# Install dependencies
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy application
COPY app.py .

# Pre-download model at build time (not runtime)
# This ensures faster startup and smaller runtime memory footprint
RUN python -c "from sentence_transformers import SentenceTransformer; SentenceTransformer('sentence-transformers/all-MiniLM-L6-v2')"

# Expose port
EXPOSE 8080

# Run the application (1 worker per machine, scale horizontally instead)
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]

