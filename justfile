# Justfile for gws-cache

default: build

# Build the gws-cache binary into bin/
build:
	mkdir -p bin
	go build -o bin/gws-cache ./cmd/gws-cache

# Run unit tests
test:
	go test -v ./...

# Run unit tests and calculate function coverage
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# View coverage in browser
coverage-html: coverage
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html
