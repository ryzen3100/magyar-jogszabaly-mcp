// hungarian-law-mcp is the single binary for all modes:
//
//	hungarian-law-mcp          MCP server over stdio (default; used by MCP clients)
//	hungarian-law-mcp serve     Streamable HTTP server (HOST/PORT env, default 127.0.0.1:3000)
package main

import (
	"fmt"
	"os"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/server"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			if err := server.RunHTTP(); err != nil {
				fmt.Fprintln(os.Stderr, "Fatal:", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
			os.Exit(2)
		}
	}
	if err := server.RunStdio(); err != nil {
		fmt.Fprintln(os.Stderr, "Fatal:", err)
		os.Exit(1)
	}
}
