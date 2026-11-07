# justfile for curfew

set quiet

binary_dir := "_bin"
project_name := "curfew"
registry := env_var_or_default("REGISTRY", "ghcr.io/paloth")
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

# Build and check targets run in Dagger (.dagger/main.go), the same functions CI calls.
# Recipes that edit files or talk to your cluster run directly on this machine.

# Build a Linux binary for this machine's architecture into _bin/ (VERSION=x.y.z to set the version)
build:
    dagger call binary --version={{version}} export --path={{local_bin}}

# Run go vet and the tests with the race detector
test:
    dagger call test

# Run tests with coverage
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated: coverage.html"

# Run a specific test
test-one pkg test:
    go test -v -run {{test}} ./{{pkg}}

# Format code
fmt:
    go fmt ./...
    gofmt -s -w .

# Run golangci-lint
lint:
    dagger call lint

# Fix linting issues where possible
lint-fix:
    golangci-lint run --fix

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

# Format, then run everything CI runs (lint, test, vuln, helm, docs, multi-arch build) in parallel
check: fmt
    dagger call check

# Scan dependencies for known vulnerabilities
vuln:
    dagger call vuln

# Lint and render the Helm chart
helm-lint:
    dagger call helm

# Build the image for this machine's architecture and load it into Docker as {{image_tag}}
docker-build:
    dagger call image --version={{version}} export-image --name={{image_tag}}

# Build the image for linux/amd64 and linux/arm64 (nothing is loaded or pushed)
docker-buildx:
    dagger call build --version={{version}}

# Push the multi-arch image as {{image_tag}}, with your local registry login (docker login)
docker-push:
    dagger call publish --version={{version}} --address={{image_tag}}

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

# Uninstall the Helm release
helm-destroy:
    -helm uninstall {{project_name}} -n {{operator_ns}}

# Build, package, and deploy to Kubernetes
deploy *helm_args: helm-destroy docker-build (helm-deploy helm_args)
    @echo "Deployed {{project_name}} {{image_tag}} to Kubernetes"

# Run every check, then push the multi-arch image at this version (CI's release workflow is the usual way)
release version:
    dagger call check
    VERSION={{version}} just docker-push

# Generate mocks (for when you add interfaces)
generate:
    go generate ./...

# Install development tools: the Dagger CLI at dagger.json's engine version, and golangci-lint for lint-fix
install-tools:
    curl -fsSL https://dl.dagger.io/dagger/install.sh | \
        DAGGER_VERSION=$(sed -n 's/.*"engineVersion": "v\(.*\)".*/\1/p' dagger.json) BIN_DIR=$HOME/.local/bin sh
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

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
    kubectl --context {{kube_context}} apply -f test/widget-crd.yaml
    helm upgrade --install {{project_name}} helm/ --kube-context {{kube_context}} -n {{operator_ns}} \
        -f test/e2e-values.yaml --set image.tag=e2e --set image.pullPolicy=Never --set dev=true --wait --timeout 2m
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

# Serve the documentation site at http://127.0.0.1:8000, reloading on edits (needs Python 3)
docs-serve: _docs-venv
    .venv-docs/bin/mkdocs serve

# Build the documentation site into site/, failing on broken links
docs-build:
    dagger call docs export --path=site

_docs-venv:
    test -x .venv-docs/bin/mkdocs || (python3 -m venv .venv-docs && .venv-docs/bin/pip install -q -r docs/requirements.txt)
