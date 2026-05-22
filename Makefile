# TPPV4 targets

ROBOT_DIR   := robot
PILOT_DIR   := pilot
BINARY      := tppv4-robot
ARM_BINARY   := tppv4-robot-arm64


# ── Local development ──────────────────────────────────────────────────────────

.PHONY: build
build:
	cd $(ROBOT_DIR) && go build -o ../$(BINARY) ./cmd/robot

.PHONY: run
run: build
	./$(BINARY)

.PHONY: test
test:
	cd $(ROBOT_DIR) && go test ./...

.PHONY: lint
lint:
	cd $(ROBOT_DIR) && go vet ./...

# ── Deploy source + build on Linux test machine ───────────────────────────────
# Requires Go to be installed on the remote machine.
# Usage:  make deploy-src LINUX_HOST=user@192.168.1.50

LINUX_HOST ?= mattmc@fuego
LINUX_DIR  := ~/tppv4

.PHONY: deploy-src
deploy-src:
	ssh $(LINUX_HOST) "mkdir -p $(LINUX_DIR)"
	rsync -av --exclude='vendor' --exclude='*.test' \
	  $(ROBOT_DIR)/ $(LINUX_HOST):$(LINUX_DIR)/$(ROBOT_DIR)/
	rsync -av $(PILOT_DIR)/ $(LINUX_HOST):$(LINUX_DIR)/$(PILOT_DIR)/
	ssh $(LINUX_HOST) "cd $(LINUX_DIR)/$(ROBOT_DIR) && go build -o ../tppv4-robot ./cmd/robot && echo 'Build OK'"
	@echo ""
	@echo "Run on $(LINUX_HOST):"
	@echo "  ssh $(LINUX_HOST)"
	@echo "  cd $(LINUX_DIR) && MAESTRO_PORT=none ./tppv4-robot"

# ── Cross-compile for Raspberry Pi (ARM64) ────────────────────────────────────

.PHONY: build-arm
build-arm:
	cd $(ROBOT_DIR) && GOOS=linux GOARCH=arm64 go build -o ../$(ARM_BINARY) ./cmd/robot

# ── Deploy to Raspberry Pi ────────────────────────────────────────────────────
# Set ROBOT_HOST to your RPi's address, e.g.:  make deploy ROBOT_HOST=pi@192.168.1.100

ROBOT_HOST ?= pi@raspberrypi.local
REMOTE_DIR := /home/pi/tppv4

.PHONY: deploy
deploy: build-arm
	ssh $(ROBOT_HOST) "mkdir -p $(REMOTE_DIR)/pilot"
	scp $(ARM_BINARY)             $(ROBOT_HOST):$(REMOTE_DIR)/$(BINARY)
	scp -r $(PILOT_DIR)/          $(ROBOT_HOST):$(REMOTE_DIR)/pilot/
	scp -r deploy/                $(ROBOT_HOST):$(REMOTE_DIR)/deploy/ 2>/dev/null || true
	ssh $(ROBOT_HOST) "sudo systemctl restart tppv4-robot || true"
	@echo "Deployed to $(ROBOT_HOST)"

.PHONY: clean
clean:
	rm -f $(BINARY) $(ARM_BINARY)
