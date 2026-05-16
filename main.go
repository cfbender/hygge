// Package main is the root hygge CLI entry point for short Go install paths.
package main

import (
	"os"

	"github.com/cfbender/hygge/cmd/hygge/cli"
)

func main() {
	os.Exit(cli.Execute())
}
