package main

import (
	"flag"
	"fmt"
	"os"

	devops "github.com/gofuego/fuego-devops"
)

func main() {
	fs := flag.NewFlagSet("fuego-devops", flag.ContinueOnError)
	siteName := fs.String("site-name", "", "site title (default: \"DevOps Docs\")")
	baseURL := fs.String("base-url", "", "base URL for the generated site")
	output := fs.String("output", "", "output directory (default: \"build\")")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: fuego-devops [flags] <repo-path> [build|serve]")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(1)
	}

	args := fs.Args()
	if len(args) < 1 {
		fs.Usage()
		os.Exit(1)
	}

	repoPath := args[0]
	command := "serve"
	if len(args) > 1 {
		command = args[1]
	}

	err := devops.Run(repoPath, devops.Options{
		SiteName: *siteName,
		BaseURL:  *baseURL,
		Output:   *output,
		Command:  command,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fuego-devops: %v\n", err)
		os.Exit(1)
	}
}
