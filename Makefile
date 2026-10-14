.PHONY: check build linux-amd64 experiments offline

check:
	go test ./...
	go test -race ./...
	CGO_ENABLED=0 go test -tags modernconly ./...
	go vet ./...
	go mod verify

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -tags modernconly -o bin/ ./cmd/...

linux-amd64:
	mkdir -p bin/linux-amd64
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags modernconly -o bin/linux-amd64/ ./cmd/...

experiments:
	mkdir -p bin/experiments
	CGO_ENABLED=0 go build -o bin/experiments/fnos-nfo ./experiments/fnos-nfo
	CGO_ENABLED=0 go build -o bin/experiments/nfo-fixtures ./experiments/nfo-fixtures
	CGO_ENABLED=0 go build -o bin/experiments/nfo-compare ./experiments/nfo-compare
	CGO_ENABLED=0 go build -o bin/experiments/metadata-nfo ./experiments/metadata-nfo

offline:
	mkdir -p work
	go run ./cmd/mediacheck < testdata/media/synthetic.json > work/offline-media.json
	CGO_ENABLED=0 go run -tags modernconly ./cmd/sqlitebench --driver modernc > work/offline-sqlite.json
