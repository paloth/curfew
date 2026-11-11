# Tracing

Set `tracing.endpoint` to an OTLP HTTP collector, such as Jaeger on port 4318:

```bash
helm upgrade --install curfew helm/ -n curfew \
  --set tracing.endpoint=jaeger-collector.observability:4318
```

Each reconcile (one check-and-fix pass on a workload) creates a `Reconcile` span with the kind, namespace and name. It has child spans `LoadState`, `SaveState` and `Scale`.

Export uses TLS by default. For a collector without TLS, such as a plain in-cluster Jaeger, add `--set tracing.insecure=true`.

The chart's NetworkPolicy opens the endpoint's port for outgoing traffic.

## Local Jaeger

From a checkout, `just jaeger-up` starts Jaeger with Docker Compose. The UI is at <http://localhost:16686>.

Your cluster must be able to reach your machine, which is often `host.docker.internal:4318` with kind or Docker Desktop. Set `tracing.insecure=true` for it.
