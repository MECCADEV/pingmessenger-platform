// Command openapi writes the schema-only OpenAPI contract. It does not start
// the FastHTTP server or register any runtime route.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"pingmessenger/internal/openapi"
)

func main() {
	output := flag.String("output", "openapi/openapi.json", "OpenAPI JSON output path")
	flag.Parse()

	document, err := openapi.Document()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build OpenAPI document:", err)
		os.Exit(1)
	}
	data, err := document.MarshalJSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal OpenAPI document:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "create OpenAPI output directory:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write OpenAPI document:", err)
		os.Exit(1)
	}
}
