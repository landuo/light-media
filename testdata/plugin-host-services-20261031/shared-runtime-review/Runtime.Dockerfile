# Isolated acceptance image: no source tree, Python, Node or old MP paths.
FROM debian:bookworm-slim@sha256:99c0b25c46d0214d5a2091092e7908907dcaecd6a962cde0ac700acee222b6f3
RUN apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 update && apt-get -o Acquire::http::Timeout=30 -o Acquire::Retries=2 install -y --no-install-recommends \
    ca-certificates tzdata libasound2 libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 \
    libcairo2 libcups2 libdbus-1-3 libdrm2 libgbm1 libglib2.0-0 libnspr4 libnss3 \
    libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 libxdamage1 libxext6 libxfixes3 \
    libxkbcommon0 libxrandr2 libudev1 libexpat1 xvfb fonts-noto-color-emoji \
    fonts-unifont libfontconfig1 libfreetype6 xfonts-scalable fonts-liberation \
    fonts-ipafont-gothic fonts-wqy-zenhei fonts-tlwg-loma-otf fonts-freefont-ttf \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1001 media && useradd --uid 1000 --gid 1001 --create-home --shell /usr/sbin/nologin media
COPY cloakbrowser-linux-x64.tar.gz /tmp/browser.tar.gz
RUN echo '4a12bcde95fa1bb1beef2b41ab5e5c27c36be78e3be3d0dac8c64d705216670e  /tmp/browser.tar.gz' | sha256sum -c - \
    && mkdir -p /usr/local/lib/light-media/cloakbrowser \
    && tar -xzf /tmp/browser.tar.gz -C /usr/local/lib/light-media/cloakbrowser \
    && rm /tmp/browser.tar.gz
COPY browser-tests-linux /usr/local/bin/browser-render-tests
ENV HOME=/home/media CLOAKBROWSER_BINARY_PATH=/usr/local/lib/light-media/cloakbrowser/chrome BROWSER_RENDER_TEST_EXECUTABLE=/usr/local/lib/light-media/cloakbrowser/chrome
USER 1000:1001
ENTRYPOINT ["/usr/local/bin/browser-render-tests"]
