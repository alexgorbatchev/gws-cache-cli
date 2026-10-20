# Justfile for gws-cache

# Default recipe: list available tasks
default:
    @just --list

# Build the gws-cache binary into bin/
build:
    @mkdir -p bin
    go build -o bin/gws-cache ./cmd/gws-cache

# Run in human-facing interactive mode
run *args: build
    ./bin/gws-cache {{ args }}

# Run in agent-facing token-conservative mode
run-ai *args: build
    AGENT=1 ./bin/gws-cache {{ args }}

# Run the unit test suite with the race detector
test:
    go test -race ./...

# Check formatting, module hygiene and static analysis
lint:
    gofmt -l .
    go mod tidy -diff
    go vet ./...

# Run lint and tests in sequence
check: lint test

# Run unit tests and report function coverage
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# View the coverage report in a browser
coverage-html: coverage
    go tool cover -html=coverage.out -o coverage.html

# Remove build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html

# Download project dependencies for local development
setup:
    go mod download
