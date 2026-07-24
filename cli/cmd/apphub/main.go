package main

import (
	"fmt"
	"os"

	"myapphub/cli/internal/cli"
)

func main() {
	if err := cli.NewRoot(cli.Dependencies{}).Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "apphub:", err)
		os.Exit(1)
	}
}
