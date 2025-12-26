// Package main is the main entrypoint for the crie CLI.
package main

import (
	"context"
	"os"

	"github.com/charmbracelet/fang"
	"github.com/tyhal/klar/internal/cli"
)

var version = ""

func main() {
	cmd := cli.Command()
	if err := fang.Execute(context.Background(), cmd, fang.WithVersion(version)); err != nil {
		os.Exit(1)
	}
}
