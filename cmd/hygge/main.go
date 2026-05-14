// Package main is the hygge CLI entry point.  All wiring lives in the
// cli subpackage; this file just dispatches and propagates the exit
// code.
package main

import (
	"os"

	"github.com/cfbender/hygge/cmd/hygge/cli"
)

func main() {
	os.Exit(cli.Execute())
}
