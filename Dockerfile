FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY internal/plugins /src/internal/plugins
RUN npm run build

FROM golang:1.26.4-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/go-dateparser ./third_party/go-dateparser
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags modernconly -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM debian:bookworm-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3
ARG TARGETARCH
# The ordinary page-source helper needs native Chromium and a virtual display.
# Install its runtime only; the application and CDP driver remain Go binaries.
RUN apt-get -o Acquire::Retries=3 update \
    && apt-get -o Acquire::Retries=3 install -y --no-install-recommends ca-certificates \
    && sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Retries=3 update \
    && apt-get -o Acquire::Retries=3 install -y --no-install-recommends \
       curl tzdata libasound2 libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 \
       libcairo2 libcups2 libdbus-1-3 libdrm2 libgbm1 libglib2.0-0 libnspr4 libnss3 \
       libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 \
       libxkbcommon0 libxrandr2 libudev1 libexpat1 xvfb fonts-noto-color-emoji \
       fonts-unifont libfontconfig1 libfreetype6 xfonts-scalable fonts-liberation \
       fonts-ipafont-gothic fonts-wqy-zenhei fonts-tlwg-loma-otf fonts-freefont-ttf \
    && groupadd --gid 1001 media \
    && useradd --uid 1000 --gid 1001 --create-home --shell /usr/sbin/nologin media \
    && rm -rf /var/lib/apt/lists/*
COPY scripts/install-browser-runtime.sh /tmp/install-browser-runtime.sh
RUN sh /tmp/install-browser-runtime.sh /usr/local/lib/light-media/cloakbrowser "$TARGETARCH" \
    && rm /tmp/install-browser-runtime.sh \
    && apt-get purge -y --auto-remove curl \
    && rm -rf /var/lib/apt/lists/*
ENV HOME=/home/media \
    CLOAKBROWSER_BINARY_PATH=/usr/local/lib/light-media/cloakbrowser/chrome \
    PATH=/usr/local/go/bin:$PATH
COPY --from=backend /usr/local/go /usr/local/go
COPY --from=backend /server /usr/local/bin/light-media
COPY --from=backend /src/third_party/go-dateparser/LICENSE /usr/share/licenses/light-media/go-dateparser.LICENSE
COPY third_party/browser-runtime /usr/share/licenses/light-media/browser-runtime
USER 1000:1001
EXPOSE 3120
VOLUME ["/config"]
ENTRYPOINT ["/usr/local/bin/light-media"]
CMD ["--listen","0.0.0.0:3120","--data-dir","/config"]
