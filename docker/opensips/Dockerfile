FROM opensips/opensips:3.6@sha256:413c4e2fd2a8c8e596f77db09bc1d18adb7cf8af61aed84d46ea32cb9c545a6d
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        opensips-postgres-module \
 && rm -rf /var/lib/apt/lists/*
