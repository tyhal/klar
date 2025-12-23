// Package main is the main entrypoint for the crie CLI.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/tyhal/klar/internal/cli"
)

var version = "latest"

func main() {
	cmd := cli.Command()
	cmd.Version = version
	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}
