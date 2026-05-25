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

# ── Cross-compile for Raspberry Pi (ARM64, no CGo) ────────────────────────────
# NOTE: CGo dependencies (libvpx, opus, malgo) require building on the RPi itself.
# Use the deploy / rpi-setup targets below for real deployments.

.PHONY: build-arm
build-arm:
	cd $(ROBOT_DIR) && GOOS=linux GOARCH=arm64 go build -o ../$(ARM_BINARY) ./cmd/robot

# ── Raspberry Pi ──────────────────────────────────────────────────────────────
# tpp.local / 192.168.1.228 — ARM64 Raspberry Pi 4B
# First-time setup:  make rpi-setup
# Deploy + run:      make deploy
#                    make deploy-run   (deploys then attaches to the process)

ROBOT_HOST ?= mattmc@tpp.local
REMOTE_DIR := ~/tppv4
GO_VER     := 1.26.3

.PHONY: rpi-setup
rpi-setup:
	@echo "==> Installing system dependencies on $(ROBOT_HOST) ..."
	ssh $(ROBOT_HOST) "sudo apt-get update && sudo apt-get install -y libvpx-dev libasound2-dev"
	@echo "==> Installing Go $(GO_VER) on $(ROBOT_HOST) ..."
	ssh $(ROBOT_HOST) " \
	  curl -fsSL https://go.dev/dl/go$(GO_VER).linux-arm64.tar.gz | sudo tar -C /usr/local -xz && \
	  grep -q '/usr/local/go/bin' ~/.profile || echo 'export PATH=\$$PATH:/usr/local/go/bin' >> ~/.profile && \
	  grep -q '/usr/local/go/bin' ~/.bashrc  || echo 'export PATH=\$$PATH:/usr/local/go/bin' >> ~/.bashrc"
	@echo ""
	@echo "==> Setup complete on $(ROBOT_HOST)."
	@echo "    Run 'make deploy' to sync source and build."

.PHONY: deploy
deploy:
	@echo "==> Syncing source to $(ROBOT_HOST):$(REMOTE_DIR) ..."
	ssh $(ROBOT_HOST) "mkdir -p $(REMOTE_DIR)"
	rsync -av --exclude='.git' --exclude='vendor' --exclude='*.test' \
	  $(ROBOT_DIR)/  $(ROBOT_HOST):$(REMOTE_DIR)/$(ROBOT_DIR)/
	rsync -av $(PILOT_DIR)/  $(ROBOT_HOST):$(REMOTE_DIR)/$(PILOT_DIR)/
	@echo "==> Building on RPi ..."
	ssh $(ROBOT_HOST) "cd $(REMOTE_DIR)/$(ROBOT_DIR) && \
	  PATH=\$$PATH:/usr/local/go/bin go build -o ../$(BINARY) ./cmd/robot && echo 'Build OK'"
	ssh $(ROBOT_HOST) "systemctl --user restart tppv4-robot && echo 'Service restarted'"
	@echo ""
	@echo "Deployed to $(ROBOT_HOST). To run:"
	@echo "  ssh $(ROBOT_HOST) 'cd $(REMOTE_DIR) && ./$(BINARY)'"
	@echo "  Pilot UI: http://tpp.local:8080"

.PHONY: deploy-run
deploy-run: deploy
	ssh -t $(ROBOT_HOST) "cd $(REMOTE_DIR) && ./$(BINARY)"

.PHONY: clean
clean:
	rm -f $(BINARY) $(ARM_BINARY)
