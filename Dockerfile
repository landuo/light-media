FROM node:24-alpine AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -tags modernconly -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -g 1001 media && adduser -D -u 1000 -G media media
COPY --from=backend /server /usr/local/bin/light-media
USER 1000:1001
EXPOSE 3120
VOLUME ["/config"]
ENTRYPOINT ["/usr/local/bin/light-media"]
CMD ["--listen","0.0.0.0:3120","--data-dir","/config"]
