// Package devops is the fuego-devops format pack: it turns a repository's
// Dockerfiles and Kubernetes manifests into an interactive infrastructure
// documentation site. Register it on any Fuego engine with
// eng.Use(devops.Pack()), or use the fuego-devops CLI (which scans a repo and
// drives the build for you).
package devops

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/gofuego/fuego-devops/graph"
	"github.com/gofuego/fuego-devops/scanner"
	"github.com/gofuego/fuego-formats/docker"
	"github.com/gofuego/fuego-formats/kubernetes"
	"github.com/gofuego/fuego/core"
	"github.com/gofuego/fuego/engine"
)

//go:embed all:theme
var themeFS embed.FS

//go:embed config-defaults.yaml
var configDefaults []byte

// Pack returns the fuego-devops format pack: the Dockerfile and Kubernetes
// parsers (imported from fuego-formats — this repo contains no parser code),
// the infrastructure theme, the resource routes + kind taxonomy as config
// defaults, and the Index hook that builds the architecture graph and
// per-namespace summary as a virtual overview page.
func Pack() core.Pack {
	theme, _ := fs.Sub(themeFS, "theme")
	return core.Pack{
		Name:           "devops",
		Parsers:        []core.Parser{docker.Parser(), kubernetes.Parser()},
		Theme:          theme,
		ConfigDefaults: configDefaults,
		Hooks: core.Hooks{
			Index: []core.IndexHook{graph.BuildOverviewHook},
		},
	}
}

// Options configures a fuego-devops site build.
type Options struct {
	SiteName string // Site title (default: "DevOps Docs")
	BaseURL  string // Base URL for the site (default: "")
	Output   string // Output directory (default: "build")
	Command  string // "build" or "serve" (default: "serve")
}

// Run scans a DevOps repository and builds (or serves) the documentation site.
// The scanner emits content files into a temporary directory; the pack
// supplies the parsers, theme, config, and graph — so Run only wires the
// engine and points it at the scanned content. No theme or config files are
// written to the repository.
func Run(repoPath string, opts Options) error {
	applyOptionDefaults(&opts)

	contentDir, err := os.MkdirTemp("", "fuego-devops-content-*")
	if err != nil {
		return fmt.Errorf("creating content dir: %w", err)
	}
	defer os.RemoveAll(contentDir)

	fmt.Println("Scanning repository...")
	if err := scanner.Run(repoPath, contentDir); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	eng := engine.New()
	eng.Use(Pack())

	bo := engine.BuildOptions{
		ContentDir: contentDir,
		OutputDir:  opts.Output,
		SiteName:   opts.SiteName,
		BaseURL:    opts.BaseURL,
	}

	fmt.Printf("\nRunning: %s\n", opts.Command)
	ctx := context.Background()
	if opts.Command == "serve" {
		return eng.Serve(ctx, bo)
	}
	return eng.Build(ctx, bo)
}

func applyOptionDefaults(opts *Options) {
	if opts.SiteName == "" {
		opts.SiteName = "DevOps Docs"
	}
	if opts.Output == "" {
		opts.Output = "build"
	}
	if opts.Command == "" {
		opts.Command = "serve"
	}
}
