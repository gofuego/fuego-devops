package parser

import (
	"strings"

	"github.com/gofuego/fuego/core"
)

// Dockerfile returns a parser for Dockerfile files.
// It implements FilenameParser to match "Dockerfile" and "Dockerfile.*" patterns.
func Dockerfile() core.Parser {
	return &dockerfileParser{}
}

type dockerfileParser struct{}

func (p *dockerfileParser) Type() string       { return "dockerfile" }
func (p *dockerfileParser) Filenames() []string { return []string{"Dockerfile", "Dockerfile.*"} }

func (p *dockerfileParser) Parse(raw []byte) (core.Envelope, []core.Node, error) {
	env, payload, err := core.SplitFrontmatter(raw)
	if err != nil {
		return nil, nil, err
	}
	if env == nil {
		env = make(core.Envelope)
	}

	lines := strings.Split(string(payload), "\n")
	var nodes []core.Node
	var currentStage string
	var images []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Comments
		if strings.HasPrefix(trimmed, "#") {
			nodes = append(nodes, core.Node{
				Type:    "comment",
				Content: strings.TrimPrefix(trimmed, "#"),
			})
			continue
		}

		// Parse instruction
		parts := strings.SplitN(trimmed, " ", 2)
		instruction := strings.ToUpper(parts[0])
		var args string
		if len(parts) > 1 {
			args = parts[1]
		}

		switch instruction {
		case "FROM":
			image, alias := parseFrom(args)
			currentStage = alias
			if image != "" {
				images = append(images, image)
			}
			nodes = append(nodes, core.Node{
				Type:    "stage",
				Content: trimmed,
				Attributes: map[string]any{
					"image": image,
					"alias": alias,
				},
			})
		default:
			attrs := map[string]any{
				"instruction": instruction,
			}
			if currentStage != "" {
				attrs["stage"] = currentStage
			}
			if instruction == "COPY" {
				if from := parseCopyFrom(args); from != "" {
					attrs["copyFrom"] = from
				}
			}
			nodes = append(nodes, core.Node{
				Type:       "instruction",
				Content:    args,
				Attributes: attrs,
			})
		}
	}

	// Enrich envelope
	if env["title"] == nil {
		if currentStage != "" {
			env["title"] = "Dockerfile (" + currentStage + ")"
		} else if len(images) > 0 {
			env["title"] = "Dockerfile — " + images[0]
		} else {
			env["title"] = "Dockerfile"
		}
	}
	if len(images) > 0 {
		env["images"] = images
	}
	env["resource_kind"] = "Dockerfile"

	return env, nodes, nil
}

// parseCopyFrom extracts the --from=<name> value from COPY args, if present.
func parseCopyFrom(args string) string {
	for _, field := range strings.Fields(args) {
		if strings.HasPrefix(field, "--from=") {
			return strings.TrimPrefix(field, "--from=")
		}
	}
	return ""
}

// parseFrom extracts the image and optional alias from a FROM line.
// e.g. "golang:1.22 AS builder" → ("golang:1.22", "builder")
func parseFrom(args string) (string, string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return "", ""
	}
	image := parts[0]
	alias := ""
	for i, p := range parts {
		if strings.EqualFold(p, "AS") && i+1 < len(parts) {
			alias = parts[i+1]
			break
		}
	}
	return image, alias
}
