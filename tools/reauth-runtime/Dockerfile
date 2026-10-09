# syntax=docker/dockerfile:1
FROM node:22.22.2-bookworm-slim AS node
FROM python:3.12.12-slim-bookworm AS runtime
ARG TOSUB2_COMMIT=8548397e89bf80e508eda64a87e0d556d43abc84
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 curl \
    && rm -rf /var/lib/apt/lists/*
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/npm
RUN ln -s /usr/local/lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm
WORKDIR /build/tosub2
RUN curl -fsSL --retry 3 "https://codeload.github.com/poxiao33/toSub2/tar.gz/$TOSUB2_COMMIT" \
    | tar -xz --strip-components=1 -C /build/tosub2
COPY tools/reauth-runtime/package*.json ./
RUN npm ci --omit=dev --ignore-scripts --no-audit --no-fund
COPY tools/reauth-runtime/requirements.txt /build/requirements.txt
RUN python -m pip install --no-cache-dir --only-binary=:all: -r /build/requirements.txt
COPY tools/openai_oauth_reauth_worker.py /build/worker.py
COPY tools/test_openai_oauth_reauth_worker.py tools/test_openai_oauth_reauth_engines.py tools/test_openai_oauth_reauth_runtime_controls.py /build/tools/
COPY tools/openai_excel_oauth_adapter.py tools/openai_excel_2fa_login.py tools/test_openai_excel_oauth_adapter.py /build/tools/
COPY tools/openai_excel_password_flow.mjs tools/test_openai_excel_password_flow.mjs /build/tools/
RUN cp /build/worker.py /build/tools/openai_oauth_reauth_worker.py \
    && cd /build && EXCEL_TEST_TOSUB2_ROOT=/build/tosub2 python -m unittest tools.test_openai_oauth_reauth_worker tools.test_openai_oauth_reauth_engines tools.test_openai_oauth_reauth_runtime_controls tools.test_openai_excel_oauth_adapter -q \
    && node /build/tools/test_openai_excel_password_flow.mjs \
    && cd /build/tosub2 && node test/password-mfa-workspace-smoke.mjs
COPY tools/reauth-runtime/package.py /build/package.py
RUN python /build/package.py /out
RUN OPENAI_REAUTH_WORKER_TOKEN=00000000000000000000000000000000 \
    TOSUB2_ROOT=/out/tosub2 NODE_EXECUTABLE=/out/node \
    /out/python /out/worker.py --check \
    && /out/python -c 'import curl_cffi; from ssl import SSLContext' \
    && /out/node -e 'require("/out/tosub2/node_modules/jsdom")'
# Validate on the same libc family as the main Alpine image, with no
# system Python or Node available to mask missing bundled dependencies.
FROM alpine:3.21 AS portable-test
COPY --from=runtime /out/ /runtime/
COPY tools/reauth-runtime/smoke.mjs /smoke.mjs
RUN NODE_EXECUTABLE=/runtime/node TOSUB2_PYTHON=/runtime/python /runtime/node /smoke.mjs \
    && touch /verified

FROM scratch AS artifact
COPY --from=runtime /out/ /
COPY --from=portable-test /verified /portable-verified
