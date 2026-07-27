//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wislist/mini-opencode/internal/app"
)

func main() {
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
