FROM python:3.12-slim
WORKDIR /svc
RUN pip install --no-cache-dir zen-engine==0.50.0
COPY risk.json service.py ./
ENV ZEN_BIND=0.0.0.0
CMD ["python","service.py"]
