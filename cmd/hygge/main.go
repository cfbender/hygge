// Package main is the entry point for the hygge CLI.
package main

import (
	"flag"
	"fmt"
	"os"
)

const version = "0.0.0-dev"

func main() {
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("hygge %s\n", version)
		os.Exit(0)
	}

	fmt.Println("not yet implemented")
}
