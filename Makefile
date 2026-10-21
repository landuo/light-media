.PHONY: check build linux-amd64 experiments offline web-build run dateparser-check

web-build:
	npm --prefix web ci --no-audit --no-fund
	npm --prefix web run build

dateparser-check:
	go -C third_party/go-dateparser test . ./date/... ./internal/...
	go -C third_party/go-dateparser test -race ./internal/data -run '^TestConcurrentFirstLocaleLookup$$' -count=1

check: web-build dateparser-check
	go test ./...
	go test -race ./...
	CGO_ENABLED=0 go test -tags modernconly ./...
	go vet ./...
	go mod verify

build: web-build
	mkdir -p bin
	CGO_ENABLED=0 go build -tags modernconly -o bin/ ./cmd/...

linux-amd64: web-build
	mkdir -p bin/linux-amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags modernconly -o bin/linux-amd64/ ./cmd/...

run: build
	./bin/server --data-dir data --listen 127.0.0.1:3120

experiments:
	mkdir -p bin/experiments
	CGO_ENABLED=0 go build -o bin/experiments/fnos-nfo ./experiments/fnos-nfo
	CGO_ENABLED=0 go build -o bin/experiments/nfo-fixtures ./experiments/nfo-fixtures
	CGO_ENABLED=0 go build -o bin/experiments/nfo-compare ./experiments/nfo-compare
	CGO_ENABLED=0 go build -o bin/experiments/metadata-nfo ./experiments/metadata-nfo
	CGO_ENABLED=0 go build -o bin/experiments/real-nfo-compare ./experiments/real-nfo-compare

offline:
	mkdir -p work
	go run ./cmd/mediacheck < testdata/media/synthetic.json > work/offline-media.json
	CGO_ENABLED=0 go run -tags modernconly ./cmd/sqlitebench --driver modernc > work/offline-sqlite.json
