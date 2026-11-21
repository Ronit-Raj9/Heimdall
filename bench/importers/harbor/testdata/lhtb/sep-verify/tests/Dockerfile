FROM python:3.11-slim
RUN python -m pip install --no-cache-dir pytest==8.4.1
ENV PIP_NO_INDEX=1
WORKDIR /app
COPY . /tests/
RUN chmod +x /tests/test.sh
CMD ["/bin/bash"]
