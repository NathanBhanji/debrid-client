// Command openapi prints the API's OpenAPI document. It is used by `make
// generate` to feed oapi-codegen and deliberately does not depend on the CLI
// (which depends on the generated client).
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/NathanBhanji/debrid-client/internal/api"
)

func main() {
	yamlOut := flag.Bool("yaml", false, "emit YAML (OpenAPI 3.0 downgrade)")
	flag.Parse()
	h := api.New(nil, api.Options{})
	var b []byte
	var err error
	if *yamlOut {
		b, err = h.Huma.OpenAPI().DowngradeYAML()
	} else {
		b, err = h.Huma.OpenAPI().Downgrade()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(b)
}
