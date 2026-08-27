// Command build-db builds the law database from data/seed JSON files —
// Go port of the TypeScript scripts/build-db.ts (kept on the dev branch).
//
// Usage:
//
//	go run ./cmd/build-db [-out data/database.db] [-seed-dir data/seed] [-eu-mappings data/eu-mappings.json]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/internal/builddb"
)

func main() {
	out := flag.String("out", "data/database.db", "output database path")
	seedDir := flag.String("seed-dir", "data/seed", "directory of seed JSON files")
	euMappings := flag.String("eu-mappings", "data/eu-mappings.json", "EU mappings JSON path")
	flag.Parse()

	if err := builddb.Build(*out, *seedDir, *euMappings, func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}); err != nil {
		fmt.Fprintln(os.Stderr, "build-db:", err)
		os.Exit(1)
	}
}
