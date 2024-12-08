APP := autotag

.PHONY: lint build test cov run

default: build

build:
	CGO_ENABLED=0 go build -trimpath -v -o $(APP)/$(APP)  $(APP)/*.go

lint:
	golangci-lint run -v ./...

test:
	go test -cover -coverprofile=cover.out -v ./...

cov:
	@echo "--- Coverage:"
	go tool cover -html=cover.out
	go tool cover -func cover.out

snapshot:
	@goreleaser release --clean --snapshot --verbose