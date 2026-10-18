// Command librarian ingests documents, analyses them and files them into an ordered archive.
package main

import (
	"fmt"
	"os"
)

// version is overridden at build time: -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("librarian", version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: librarian <command>\n\ncommands:\n  version   print version")
}
