package main

import (
	"fmt"
	"os"

	"github.com/lukeisontheroad/ivanti-neurons-cli/internal/cmd"
)

// Set via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if err := cmd.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "ivanti-neurons:", err)
		os.Exit(1)
	}
}
