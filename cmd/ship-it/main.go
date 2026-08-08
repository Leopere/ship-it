package main

import (
	"fmt"
	"os"

	"github.com/Leopere/ship-it/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(os.Args[1:], version, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ship-it:", err)
		os.Exit(1)
	}
}
