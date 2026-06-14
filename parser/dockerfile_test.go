package parser

import (
	"testing"

	"github.com/gofuego/fuego/core"
)

func TestDockerfileParser_Type(t *testing.T) {
	p := Dockerfile()
	if p.Type() != "dockerfile" {
		t.Errorf("expected type 'dockerfile', got %q", p.Type())
	}
}

func TestDockerfileParser_Filenames(t *testing.T) {
	p := Dockerfile()
	fp, ok := p.(core.FilenameParser)
	if !ok {
		t.Fatal("expected FilenameParser interface")
	}
	names := fp.Filenames()
	if len(names) != 2 || names[0] != "Dockerfile" {
		t.Errorf("unexpected filenames: %v", names)
	}
}

func TestDockerfileParser_Basic(t *testing.T) {
	raw := []byte(`FROM golang:1.22 AS builder
RUN go build -o /app
FROM alpine:3.19
COPY --from=builder /app /app
EXPOSE 8080
CMD ["/app"]
`)

	p := Dockerfile()
	env, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env["resource_kind"] != "Dockerfile" {
		t.Errorf("expected resource_kind 'Dockerfile', got %v", env["resource_kind"])
	}

	images := env["images"].([]string)
	if len(images) != 2 || images[0] != "golang:1.22" || images[1] != "alpine:3.19" {
		t.Errorf("unexpected images: %v", images)
	}

	// Should have 2 stages + 4 instructions = 6 nodes
	if len(nodes) != 6 {
		t.Fatalf("expected 6 nodes, got %d", len(nodes))
	}

	if nodes[0].Type != "stage" {
		t.Errorf("expected first node to be stage, got %q", nodes[0].Type)
	}
	if nodes[0].Attributes["image"] != "golang:1.22" {
		t.Errorf("expected image 'golang:1.22', got %v", nodes[0].Attributes["image"])
	}
	if nodes[0].Attributes["alias"] != "builder" {
		t.Errorf("expected alias 'builder', got %v", nodes[0].Attributes["alias"])
	}

	// Check COPY --from=builder instruction
	copyNode := nodes[3] // stage, RUN, stage, COPY
	if copyNode.Attributes["copyFrom"] != "builder" {
		t.Errorf("expected copyFrom 'builder', got %v", copyNode.Attributes["copyFrom"])
	}
}

func TestDockerfileParser_WithComments(t *testing.T) {
	raw := []byte(`# Build stage
FROM golang:1.22
RUN go build
`)

	p := Dockerfile()
	_, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(nodes))
	}
	if nodes[0].Type != "comment" {
		t.Errorf("expected comment node, got %q", nodes[0].Type)
	}
}

func TestDockerfileParser_WithFrontmatter(t *testing.T) {
	raw := []byte(`---
title: Custom Title
---
FROM node:18
RUN npm install
`)

	p := Dockerfile()
	env, nodes, err := p.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if env["title"] != "Custom Title" {
		t.Errorf("expected custom title, got %v", env["title"])
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}
