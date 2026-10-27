# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Curfew is a Kubernetes operator that scales workloads (Deployments, StatefulSets, and extra kinds with a `/scale` subresource) to zero, and suspends CronJobs, based on cron schedule annotations. The project uses Go and controller-runtime.

## Development Commands

### Building
```bash
go build                    # Build the binary
go build -o curfew     # Build with specific output name
```

### Testing
```bash
go test ./...              # Run all tests
go test -v ./...           # Run tests with verbose output
go test ./internal/curfew    # Test specific package
just lint                  # golangci-lint v2 (install with `just install-tools`)
```

### Dependencies
```bash
go mod tidy                # Clean up dependencies
go mod download            # Download dependencies
go get <package>           # Add new dependency
```

### Observability and Tracing
```bash
just jaeger-up             # Start Jaeger for local development (docker compose)
just jaeger-ui             # Open Jaeger UI (http://localhost:16686)
just jaeger-down           # Stop Jaeger
```

## Architecture

### Project Structure
- **main.go**: Entry point that delegates to cmd.Execute()
- **cmd/root.go**: parses the one flag (`--dev`, standard `flag` package), sets up logging and tracing, then runs the operator

### Configuration
No config file. The operator reads:
- `--dev` flag: debug logs (Helm value `dev`)
- `POD_NAMESPACE` env (required): where the state ConfigMap and leader lease live (set by the chart via the downward API)
- `JAEGER_ENDPOINT` env (optional): enables tracing (Helm value `tracing.endpoint`)
- `OTEL_EXPORTER_OTLP_INSECURE=true` env (optional): plain HTTP for traces instead of TLS (Helm value `tracing.insecure`)
- `METRICS_ADDR` env (optional, e.g. `:8080`): Prometheus metrics, off when unset (Helm value `metrics.enabled`); curfew's own metrics are in `internal/curfew/metrics.go`
- `DRY_RUN=true` env (optional): workload writes are sent with dryRun=All, curfew-state is still written, waves don't wait (Helm value `dryRun`)
- `EXTRA_KINDS` env (optional, comma-separated `Kind.version.group`): more kinds with a `/scale` subresource, handled as unstructured objects (Helm value `extraKinds`, which also adds their RBAC rules; `extraKinds` in `internal/curfew/curfew.go`)
- `WATCH_NAMESPACES` env (optional, comma-separated): only watch these namespaces (Helm value `watchNamespaces`, which also swaps the ClusterRoleBinding for per-namespace RoleBindings)

### Operator
Runs in-cluster only, built on controller-runtime (`internal/curfew`), with leader election always on.
- Workloads opt in with annotations (`internal/values`):
  - `curfew/sleep`: cron expression, when to scale to 0
  - `curfew/wake`: cron expression, when to restore
  - `curfew/timezone`: IANA zone, default UTC
- A namespace can carry the same annotations to schedule all its workloads, plus `curfew/exclude-kinds` and `curfew/exclude-labels`; a workload's own `curfew/sleep` always wins (`scheduleFor` in `internal/curfew/schedule.go`). Namespace annotation changes re-reconcile the namespace's workloads (`workloadsIn`).
- `curfew/skip-dates` (comma-separated `YYYY-MM-DD`, part of the schedule's annotations) skips sleep and wake times on those dates, read in the schedule's time zone (`evaluate`).
- `curfew/awake-until` (RFC 3339) on a workload, else its namespace, keeps it awake until then (`desired` in `internal/curfew/schedule.go`, which Reconcile and waves both use).
- `curfew/wave` (integer, default 0, negative allowed) orders workloads within a namespace: on sleep a workload waits until lower waves going to sleep have no pods (or running Jobs) left, on wake until higher waves waking up are ready; it polls every 10s and gives up after 10 min (`internal/curfew/wave.go`).
- A sleeping workload found scaled up again is scaled back to 0 and counted (`scaledUpWhileAsleep` in `internal/curfew/conflict.go`); at 3 in one sleep window curfew logs that another controller is fighting it.
- Namespaces starting with `kube-` are ignored (`scheduleFor`, `scheduled`).
- Deployments, StatefulSets and extra kinds scale only through the `/scale` subresource (extra kinds through an unstructured body, `unstructuredScale`); CronJobs are suspended by patching `spec.suspend` (no subresource exists), counted as 1 replica running / 0 suspended (`current` in `internal/curfew/curfew.go`).
- Original replica counts live in the `curfew-state` ConfigMap in the operator's namespace (`POD_NAMESPACE`), keyed `<kind>.<namespace>.<name>`.
- Each reconcile requeues itself at the next sleep or wake time; removing the annotations wakes the workload.
- Health probes: `:9090/healthz` and `/readyz`.

### OpenTelemetry Integration
The project uses OpenTelemetry for distributed tracing with Jaeger as the backend:

**Architecture:**
- **internal/telemetry/tracing.go**: Initializes the OpenTelemetry tracer provider
  - Configures OTLP HTTP exporter for Jaeger (port 4318)
  - Sets up resource attributes (service name, version)
- **cmd/root.go**: Initializes tracing on startup when `JAEGER_ENDPOINT` is set, and flushes spans on shutdown
- **internal/curfew/curfew.go**: Spans `Reconcile` (kind, namespace, name, asleep), with children `LoadState`, `SaveState` and `Scale`; errors mark the span failed via `endSpan`

**Usage Pattern:**
```go
// Get a tracer once per package (works even before InitTracer runs)
var tracer = otel.Tracer("curfew/internal/package-name")

// Create a span
ctx, span := tracer.Start(ctx, "operation-name")
defer span.End()

// Add attributes
span.SetAttributes(
    attribute.String("key", "value"),
)

// Record errors
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, "error message")
}
```

**Configuration:**
- `JAEGER_ENDPOINT` environment variable (OTLP HTTP `host:port`); unset means tracing is off (no-op tracer)
- Helm: set `tracing.endpoint` in values, e.g. `jaeger-collector.observability:4318`
- Export uses TLS unless `OTEL_EXPORTER_OTLP_INSECURE=true`; the endpoint must be reachable from the operator pod (and allowed by the chart's NetworkPolicy, which opens the endpoint's port)
- `service.version` comes from `cmd.Version`, stamped at build time (`-X curfew/cmd.Version=...`, `VERSION` build arg)
- Use `just jaeger-up` to start local Jaeger instance

### Future Implementation Notes
- Spans added to reconciliation loops, API calls, and scaling operations
