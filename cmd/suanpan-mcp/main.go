// suanpan-mcp — MCP server exposing suanpan's accounting operations as tools.
//
// Transports (pick one via flag):
//
//	(default)      JSON-RPC 2.0 over stdio, newline-delimited frames.
//	-listen ADDR   Streamable HTTP on /mcp (POST request/response, GET SSE,
//	               DELETE to close). Supports many concurrent clients.
//
// Register the stdio variant with Claude Code via:
//
//	{
//	  "mcpServers": {
//	    "suanpan": {
//	      "command": "/absolute/path/to/suanpan-mcp",
//	      "env": { "SUANPAN_DB": "/absolute/path/to/suanpan.db" }
//	    }
//	  }
//	}
//
// For the HTTP variant, run `suanpan-mcp -listen localhost:7777` and point
// your client at http://localhost:7777/mcp. Set SUANPAN_TOKEN to require a
// bearer token on every request (recommended if binding to non-loopback).
package main

import (
	"flag"
	"log"
	"os"

	"github.com/goose/suanpan/internal/store"
)

var logger = log.New(os.Stderr, "suanpan-mcp: ", log.LstdFlags)

func main() {
	var listen string
	flag.StringVar(&listen, "listen", "", "HTTP listen address (e.g. 'localhost:7777'). Empty = stdio.")
	flag.Parse()

	s, err := store.Open("")
	if err != nil {
		logger.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.Init(); err != nil {
		logger.Fatalf("init store: %v", err)
	}

	sv := newServer(s)
	if listen != "" {
		if err := sv.serveHTTP(listen); err != nil {
			logger.Fatalf("http: %v", err)
		}
		return
	}
	if err := sv.serveStdio(); err != nil {
		logger.Fatalf("stdio: %v", err)
	}
}
