# justfile for curfew

set quiet

# Variables
binary_dir := "_bin"
project_name := "curfew"
registry := env_var_or_default("REGISTRY", "paloth")
version := env_var_or_default("VERSION", "latest")
local_bin:= binary_dir + "/" + project_name
image_tag := registry + "/" + project_name + ":" + version
# Dedicated namespace: whoever can write ConfigMaps or Leases here can steer the operator
operator_ns := env_var_or_default("OPERATOR_NS", project_name)

alias bb := build
alias db := docker-build
alias t := test
alias hdp := helm-deploy
alias hds := helm-destroy

# Show available commands
default:
    @just --list

# Build a Linux binary for this machine's architecture (the Docker image builds its own)
build:
    GOOS=linux go build -o {{local_bin}} .

# Build with version information
build-versioned version="dev":
    go build -ldflags "-X curfew/cmd.Version={{version}}" -o {{local_bin}} .

# Run all tests
test:
    go test -v ./...

# Run tests with coverage
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated: coverage.html"

# Run tests with race detection
test-race:
    go test -v -race ./...

# Run a specific test
test-one pkg test:
    go test -v -run {{test}} ./{{pkg}}

# Format code
fmt:
    go fmt ./...
    gofmt -s -w .

# Run linter
lint:
    golangci-lint run

# Fix linting issues where possible
lint-fix:
    golangci-lint run --fix

# Vet code
vet:
    go vet ./...

# Tidy dependencies
tidy:
    go mod tidy
    go mod verify

# Download dependencies
deps:
    go mod download

# Clean build artifacts
clean: helm-destroy
    rm -f {{local_bin}}
    rm -f coverage.out coverage.html
    go clean -cache -testcache
    docker rmi -f $(docker images -f "reference={{registry}}/{{project_name}}" -q)

# Run all checks (fmt, vet, lint, test)
check: fmt vet lint test vuln

# Scan dependencies for known vulnerabilities
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Build Docker image
docker-build:
    docker build --build-arg VERSION={{version}} -t {{image_tag}} .

# Build Docker image with BuildKit
docker-build-buildkit:
    DOCKER_BUILDKIT=1 docker build --build-arg VERSION={{version}} -t {{image_tag}} .

# Build multi-arch Docker image
docker-buildx:
    docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION={{version}} -t {{image_tag}} .

# Push Docker image to registry
docker-push: docker-build
    docker push {{image_tag}}

# Build and push Docker image
docker-release: docker-build docker-push
    @echo "Released {{image_tag}}"

# Run Docker container locally
docker-run:
    docker run --rm -it {{image_tag}}

# Deploy with Helm
helm-deploy *args: _namespace
    helm upgrade --install {{project_name}} -n {{operator_ns}} {{args}} -f helm/values.yaml helm/

# Create the operator namespace with the "restricted" Pod Security level (extra args go to kubectl)
_namespace *kubectl_args:
    kubectl {{kubectl_args}} create namespace {{operator_ns}} --dry-run=client -o yaml | kubectl {{kubectl_args}} apply -f -
    kubectl {{kubectl_args}} label namespace {{operator_ns}} --overwrite \
        pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/warn=restricted

# Apply Kubernetes manifests
helm-destroy:
    -helm uninstall {{project_name}} -n {{operator_ns}}

# Build, package, and deploy to Kubernetes
deploy *helm_args: helm-destroy docker-build (helm-deploy helm_args)
    @echo "Deployed {{project_name}} {{image_tag}} to Kubernetes"

# Complete release process with tests
release version:
    @echo "Running tests..."
    just test
    @echo "Building version {{version}}..."
    VERSION={{version}} just build-versioned {{version}}
    @echo "Building and pushing Docker image..."
    VERSION={{version}} just docker-push
    @echo "Release {{version}} complete!"

# Development workflow - run checks before commit
pre-commit: fmt vet lint test
    @echo "Pre-commit checks passed ✓"

# Generate mocks (for when you add interfaces)
generate:
    go generate ./...

# Install development tools
install-tools:
    @echo "Installing golangci-lint..."
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
    @echo "Installing tools complete!"

# Show dependency tree
deps-tree:
    go mod graph

# Check for outdated dependencies
deps-outdated:
    go list -u -m all

# Upgrade all dependencies
deps-upgrade:
    go get -u ./...
    go mod tidy

# Start Jaeger for local development (UI on :16686, OTLP HTTP on :4318)
jaeger-up:
    docker compose up -d jaeger

# Open the Jaeger UI
jaeger-ui:
    open http://localhost:16686

# Stop Jaeger
jaeger-down:
    docker compose down

# --- Local cluster on Colima (k3s). Images built with Colima's Docker are visible to k3s, no registry needed.

kube_context := env_var_or_default("KUBE_CONTEXT", "colima")

# Start Colima with Kubernetes (k3s)
colima-up:
    colima start --kubernetes

# Build the image into Colima's Docker and install or upgrade curfew on its k3s
colima-deploy:
    DOCKER_CONTEXT=colima VERSION=e2e just docker-build
    just _namespace --context {{kube_context}}
    helm upgrade --install {{project_name}} helm/ --kube-context {{kube_context}} -n {{operator_ns}} \
        --set image.tag=e2e --set image.pullPolicy=Never --set dev=true --wait --timeout 2m
    # Same tag on every build: restart so the pod runs the image just built
    kubectl --context {{kube_context}} -n {{operator_ns}} rollout restart deployment/{{project_name}}
    kubectl --context {{kube_context}} -n {{operator_ns}} rollout status deployment/{{project_name}} --timeout 2m

# Follow the operator logs on Colima
colima-logs:
    kubectl --context {{kube_context}} -n {{operator_ns}} logs -f deployment/{{project_name}}

# Uninstall curfew from Colima (the curfew-state ConfigMap is kept)
colima-undeploy:
    -helm uninstall {{project_name}} --kube-context {{kube_context}} -n {{operator_ns}}

# Deploy to Colima and run the end-to-end test (about 5 minutes)
e2e: colima-deploy
    KUBE_CONTEXT={{kube_context}} OPERATOR_NS={{operator_ns}} test/e2e.sh
