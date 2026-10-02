package main

import (
	"fmt"
	"os"

	"github.com/teemuteemu/tf-mankeli/pkg/ui"
)

func main() {
	// Read the state of the given Terraform directory, or the current one.
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	if err := ui.Run(dir); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
