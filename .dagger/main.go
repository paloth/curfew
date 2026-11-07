// CI for curfew: lint, test, vuln scan, Helm checks and the image build.
//
// Every function runs on the repository root by default, so `dagger call lint`
// works with no arguments, locally and in GitHub Actions.
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/curfew-ci/internal/dagger"

	"github.com/containerd/platforms"

	"golang.org/x/sync/errgroup"
)

// Tool images are pinned by digest like the Dockerfile's base images.
// Refresh: docker buildx imagetools inspect <image:tag>, and copy the top "Digest:".
const (
	goImage   = "golang:1.27@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e" // glibc, for -race
	lintImage = "golangci/golangci-lint:v2.15.0@sha256:fbfcfd12a6f8d7f1b97b3362ca80fc2a244d46863148c73cb88e7793af96c481"
	helmImage = "alpine/helm:4.3.1@sha256:e3f8d5c462d75470fbb1fce79ad5c20063657096d1f98c0fcea869e2600e2e5e"
	govulnVer = "v1.9.0"
)

var released = []dagger.Platform{"linux/amd64", "linux/arm64"}

type CurfewCi struct {
	Src *dagger.Directory
}

func New(
	// +defaultPath="/"
	// +ignore=[".git", "_bin", ".venv-docs", "site", ".dagger"]
	src *dagger.Directory,
) *CurfewCi {
	return &CurfewCi{Src: src}
}

func (m *CurfewCi) goEnv(image string) *dagger.Container {
	return dag.Container().From(image).
		WithMountedCache("/go/pkg/mod", dag.CacheVolume("curfew-gomod")).
		WithMountedCache("/root/.cache/go-build", dag.CacheVolume("curfew-gobuild")).
		WithMountedDirectory("/src", m.Src).
		WithWorkdir("/src")
}

// Run golangci-lint
func (m *CurfewCi) Lint(ctx context.Context) (string, error) {
	return m.goEnv(lintImage).
		WithMountedCache("/root/.cache/golangci-lint", dag.CacheVolume("curfew-golangci")).
		WithExec([]string{"golangci-lint", "run"}).
		Stdout(ctx)
}

// Run go vet and the tests with the race detector
func (m *CurfewCi) Test(ctx context.Context) (string, error) {
	return m.goEnv(goImage).
		WithExec([]string{"go", "vet", "./..."}).
		WithExec([]string{"go", "test", "-race", "./..."}).
		Stdout(ctx)
}

// Scan dependencies for known vulnerabilities
func (m *CurfewCi) Vuln(ctx context.Context) (string, error) {
	return m.goEnv(goImage).
		WithExec([]string{"go", "run", "golang.org/x/vuln/cmd/govulncheck@" + govulnVer, "./..."}).
		Stdout(ctx)
}

// Lint the chart and render it with the value combinations CI cares about
func (m *CurfewCi) Helm(ctx context.Context) (string, error) {
	return dag.Container().From(helmImage).
		WithMountedDirectory("/src", m.Src).
		WithWorkdir("/src").
		WithExec([]string{"helm", "lint", "helm/"}).
		WithExec([]string{"helm", "template", "ci", "helm/"}).
		WithExec([]string{"helm", "template", "ci", "helm/", "--set", "watchNamespaces={dev,staging}",
			"--set", "tracing.endpoint=jaeger:4318", "--set", "tracing.insecure=true", "--set", "dev=true"}).
		WithExec([]string{"helm", "template", "ci", "helm/", "--set", "networkPolicy.enabled=false"}).
		Stdout(ctx)
}

// Build the operator image for one platform, like the Dockerfile does (same base images).
// The binary is cross-compiled on the engine's own platform: DockerBuild sets
// BUILDPLATFORM to the target, which runs Go under emulation and crashes.
func (m *CurfewCi) Image(
	ctx context.Context,
	// +default="dev"
	version string,
	// +default="linux/amd64"
	platform dagger.Platform,
) (*dagger.Container, error) {
	spec, err := platforms.Parse(string(platform))
	if err != nil {
		return nil, err
	}
	bases, err := m.baseImages(ctx)
	if err != nil {
		return nil, err
	}
	bin := m.goEnv(bases[0]).
		WithEnvVariable("CGO_ENABLED", "0").
		WithEnvVariable("GOOS", spec.OS).
		WithEnvVariable("GOARCH", spec.Architecture).
		WithExec([]string{"go", "build", "-ldflags=-w -s -X curfew/cmd.Version=" + version, "-o", "/out/curfew", "."}).
		File("/out/curfew")
	return dag.Container(dagger.ContainerOpts{Platform: platform}).From(bases[1]).
		WithFile("/usr/local/bin/curfew", bin).
		WithUser("65532:65532"). // numeric: runAsNonRoot can only verify a numeric UID
		WithEntrypoint([]string{"/usr/local/bin/curfew"}), nil
}

// The Dockerfile's builder and runtime images, so Dependabot bumps reach both builds
func (m *CurfewCi) baseImages(ctx context.Context) ([]string, error) {
	dockerfile, err := m.Src.File("Dockerfile").Contents(ctx)
	if err != nil {
		return nil, err
	}
	var images []string
	for line := range strings.Lines(dockerfile) {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "FROM" {
			continue
		}
		if strings.HasPrefix(f[1], "--platform=") {
			f = f[1:]
		}
		images = append(images, f[1])
	}
	if len(images) != 2 {
		return nil, fmt.Errorf("Dockerfile: want 2 FROM images (builder, runtime), got %d", len(images))
	}
	return images, nil
}

// Build the image for every released platform (linux/amd64, linux/arm64)
func (m *CurfewCi) Build(
	ctx context.Context,
	// +default="dev"
	version string,
) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, p := range released {
		g.Go(func() error {
			img, err := m.Image(ctx, version, p)
			if err == nil {
				_, err = img.Sync(ctx)
			}
			return err
		})
	}
	return g.Wait()
}

// Run lint, test, vuln, helm and the multi-platform build in parallel
func (m *CurfewCi) Check(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	for _, f := range []func(context.Context) (string, error){m.Lint, m.Test, m.Vuln, m.Helm} {
		g.Go(func() error { _, err := f(ctx); return err })
	}
	g.Go(func() error { return m.Build(ctx, "dev") })
	return g.Wait()
}
