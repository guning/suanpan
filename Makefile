BIN_DIR ?= $(HOME)/.local/bin
GO ?= go
DEMO_DB ?= /tmp/suanpan-demo.db

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

# Wipe and re-seed a throwaway DB, then run a tiny end-to-end scenario covering
# the read-scope contract (-as-person is required for report).
demo: build
	rm -f $(DEMO_DB) $(DEMO_DB)-wal $(DEMO_DB)-shm
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan init
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan family add -name 示例家庭
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan person add -name 小明 -family 1
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan txn add -amount 35    -kind expense -category-name 餐饮 -person 1 -payee 沙县小吃
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan txn add -amount 12000 -kind income  -category-name 工资 -person 1
	SUANPAN_DB=$(DEMO_DB) ./bin/suanpan report -as-person 1
	rm -f $(DEMO_DB) $(DEMO_DB)-wal $(DEMO_DB)-shm

# Run the MCP server over HTTP on localhost:7777 (many clients supported).
serve-http: build
	./bin/suanpan-mcp -listen localhost:7777
