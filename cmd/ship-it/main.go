package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/Leopere/ship-it/internal/app"
)

var version = "dev"

func main() {
	var input io.Reader
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		reader := bufio.NewReader(os.Stdin)
		// A runner's empty pipe is a bare invocation. Nonempty input remains
		// a hook payload, including malformed input that must not ship.
		if _, err := reader.Peek(1); err != io.EOF {
			input = reader
		}
	}
	if err := app.RunInput(os.Args[1:], version, input, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ship-it:", err)
		os.Exit(1)
	}
}
