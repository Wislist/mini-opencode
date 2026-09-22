//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wislist/mini-opencode/internal/app"
)

// version is injected at build time by the Makefile:
//
//	-ldflags "-X main.version=..."
//
// It stays empty under a plain `go build`, in which case app falls back to the
// version constant in the source tree.
var version string

func main() {
	app.SetVersion(version)

	ctx := context.Background()

	if app.IsTerminal(os.Stdin) {
		if err := app.RunTUI(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "mini-opencode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := app.Run(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "mini-opencode: %v\n", err)
		os.Exit(1)
	}
}
