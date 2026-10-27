# Development

Requirements: Go 1.27, [just](https://github.com/casey/just), Docker and Helm. Python 3 for this documentation site.

```bash
just test            # go test -v ./...
just test-race       # with the race detector
just lint            # golangci-lint v2
just vuln            # govulncheck: known vulnerabilities in dependencies
just check           # fmt, vet, lint, test, vuln
just install-tools   # install golangci-lint v2
just docker-build    # build the image (VERSION=x.y.z to set the version)
just docker-buildx   # multi-arch image (linux/amd64, linux/arm64)
just helm-deploy     # create the labeled namespace, install or upgrade the chart
just deploy          # uninstall, rebuild the image, reinstall
```

The namespace defaults to `curfew`. Set `OPERATOR_NS` to change it.

## Local cluster with Colima

[Colima](https://github.com/abiosoft/colima) runs a local Kubernetes (k3s). Images built with Colima's Docker are available to it without a registry.

```bash
just colima-up         # colima start --kubernetes
just e2e               # build, deploy, then run test/e2e.sh (about 5 minutes)
just colima-logs       # follow the operator logs
just colima-undeploy   # uninstall (the curfew-state ConfigMap is kept)
```

`test/e2e.sh` runs in a throwaway `curfew-e2e` namespace and checks that:

- a Deployment, a StatefulSet and a CronJob sleep and wake on real cron times a few minutes ahead, and their counts are stored then cleared,
- the StatefulSet, in wave 1, waits for the Deployment's pods to be gone,
- `curfew/awake-until` wakes a sleeping workload, and removing the annotations wakes it right away,
- a namespace schedule applies, with StatefulSets excluded,
- a `Widget` custom resource (`test/widget-crd.yaml`, enabled through `test/e2e-values.yaml`) sleeps and wakes through `/scale`.

`KUBE_CONTEXT` (default `colima`) selects another cluster. CI runs the same test on kind.

## Documentation

This site is built with [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/) from `docs/` and `mkdocs.yml`, and published to GitHub Pages on every push to `main`.

```bash
just docs-serve   # http://127.0.0.1:8000, reloads on edits
just docs-build   # build into site/, failing on broken links
```

## Code layout

- `cmd/root.go`: CLI entry point, logging and tracing setup
- `internal/curfew/`:
    - `curfew.go`: controller setup and the reconcile loop
    - `schedule.go`: which rule applies and when (schedules, exclusions, holidays, keep awake)
    - `wave.go`: waves
    - `metrics.go`, `conflict.go`: metrics and detection of other controllers
- `internal/values/`: annotation names and constants
- `internal/telemetry/`: OpenTelemetry tracer setup
- `helm/`: the chart
- `mocks/`: sample annotated manifests for manual testing
- `test/`: end-to-end test and its fixtures
