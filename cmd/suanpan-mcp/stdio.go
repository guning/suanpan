package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// serveStdio reads newline-delimited JSON-RPC requests from stdin and writes
// responses to stdout. Returns on EOF.
func (sv *server) serveStdio() error {
	reader := bufio.NewReaderSize(os.Stdin, 1<<20)
	writer := bufio.NewWriter(os.Stdout)

	send := func(resp *rpcResponse) {
		b, err := json.Marshal(resp)
		if err != nil {
			logger.Printf("marshal response: %v", err)
			return
		}
		writer.Write(b)
		writer.WriteByte('\n')
		writer.Flush()
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) == 0 && err == io.EOF {
			return nil
		}
		if err != nil && err != io.EOF {
			return err
		}
		line = trimTrailingNewline(line)
		if len(line) == 0 {
			if err == io.EOF {
				return nil
			}
			continue
		}
		var req rpcRequest
		if jerr := json.Unmarshal(line, &req); jerr != nil {
			logger.Printf("parse: %v (%q)", jerr, line)
			if err == io.EOF {
				return nil
			}
			continue
		}
		resp := sv.dispatch(&req)
		if resp != nil {
			send(resp)
		}
		if req.Method == "shutdown" {
			return nil
		}
		if err == io.EOF {
			return nil
		}
	}
}

func trimTrailingNewline(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
