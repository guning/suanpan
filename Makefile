BIN_DIR ?= $(HOME)/.local/bin
GO ?= go

.PHONY: build install clean test demo serve-http

build:
	$(GO) build -o bin/suanpan     ./cmd/suanpan
	$(GO) build -o bin/suanpan-mcp ./cmd/suanpan-mcp

install: build
	mkdir -p $(BIN_DIR)
	install -m 0755 bin/suanpan      $(BIN_DIR)/suanpan
	install -m 0755 bin/suanpan-mcp  $(BIN_DIR)/suanpan-mcp
	@echo "installed to $(BIN_DIR)"

clean:
	rm -rf bin

test:
	$(GO) vet ./...
	$(GO) test ./...

# Wipe and re-seed a throwaway DB, then run a tiny scenario.
demo: build
	rm -f /tmp/suanpan-demo.db
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan init
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan family add -name 示例家庭
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan person add -name 小明 -family 1
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan account add -name 招行 -type bank -owner person:1 -initial 5000
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan txn add -amount 35 -account 1 -kind expense -category-name 餐饮
	SUANPAN_DB=/tmp/suanpan-demo.db ./bin/suanpan report
	rm -f /tmp/suanpan-demo.db /tmp/suanpan-demo.db-wal /tmp/suanpan-demo.db-shm

# Run the MCP server over HTTP on localhost:7777 (many clients supported).
serve-http: build
	./bin/suanpan-mcp -listen localhost:7777
