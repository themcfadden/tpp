package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mattmc/tppv4/robot/config"
	"github.com/mattmc/tppv4/robot/internal/control"
	"github.com/mattmc/tppv4/robot/internal/hardware/maestro"
	"github.com/mattmc/tppv4/robot/internal/hardware/motor"
	"github.com/mattmc/tppv4/robot/internal/media"
	webrtcpeer "github.com/mattmc/tppv4/robot/internal/webrtc"
)

func main() {
	configPath := resolveConfigPath()
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("config: loaded %s", configPath)
	log.Printf(
		"config: camera pan/tilt channels=%d/%d laser pan/tilt/on channels=%d/%d/%d",
		cfg.MaestroPanChannel,
		cfg.MaestroTiltChannel,
		cfg.MaestroLaserPanChannel,
		cfg.MaestroLaserTiltChannel,
		cfg.MaestroLaserChannel,
	)

	// Maestro servo controller -- non-fatal if port unavailable (e.g. dev machine)
	var maestroCtrl *maestro.Maestro
	if cfg.MaestroPort != "" && cfg.MaestroPort != "none" {
		maestroCtrl, err = maestro.New(cfg.MaestroPort)
		if err != nil {
			log.Printf("maestro: WARNING -- could not open %s: %v (servo/laser disabled)", cfg.MaestroPort, err)
		} else {
			defer maestroCtrl.Close()
			_ = maestroCtrl.Center(uint8(cfg.MaestroPanChannel))
			_ = maestroCtrl.Center(uint8(cfg.MaestroTiltChannel))
			_ = maestroCtrl.SetLaser(uint8(cfg.MaestroLaserChannel), false)
			log.Printf("maestro: connected on %s, servos centered", cfg.MaestroPort)
		}
	} else {
		log.Printf("maestro: no port configured -- servo/laser disabled")
	}
	if maestroCtrl == nil {
		maestroCtrl = &maestro.Maestro{} // zero value -- all methods are no-ops when port is nil
	}

	// Motor controller
	var motorCtrl motor.MotorController
	switch cfg.MotorDriver {
	case "uart":
		motorCtrl, err = motor.NewUARTMotorController(cfg.MotorPort, cfg.MotorBaud)
		if err != nil {
			log.Fatalf("motor: %v", err)
		}
		defer motorCtrl.Close()
		log.Printf("motor: UART driver on %s @ %d baud", cfg.MotorPort, cfg.MotorBaud)
	default:
		motorCtrl = &motor.NoOpMotorController{}
		log.Printf("motor: using no-op driver (set MOTOR_DRIVER=uart for real hardware)")
	}

	// Command dispatcher: routes data channel messages to hardware
	dispatcher := control.New(cfg, maestroCtrl, motorCtrl)

	// Media: camera + microphone capture
	mediaCtrl, err := media.New(cfg)
	if err != nil {
		// Non-fatal on dev machines without camera hardware
		log.Printf("media: WARNING -- camera/mic unavailable: %v", err)
		mediaCtrl = media.NewStub()
	}

	// WebRTC peer manager
	peerManager := webrtcpeer.New(cfg, dispatcher, mediaCtrl)

	// Pilot dir: configurable so the binary can be run from any directory.
	// Default "pilot" resolves relative to cwd (works with `make run` from repo root).
	pilotDir := os.Getenv("PILOT_DIR")
	if pilotDir == "" {
		pilotDir = "pilot"
	}

	mux := http.NewServeMux()
	mux.Handle("/ws", peerManager)
	mux.Handle("/display-ws", http.HandlerFunc(peerManager.ServeDisplay))
	mux.HandleFunc("/display-health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(peerManager.DisplayHealthSnapshot())
	})
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		http.ServeFile(w, r, pilotDir+"/favicon.svg")
	})
	mux.HandleFunc("/display", func(w http.ResponseWriter, r *http.Request) {
		// Prevent Chromium kiosk from caching display.html so updates are always picked up.
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, pilotDir+"/display.html")
	})
	mux.Handle("/", http.FileServer(http.Dir(pilotDir)))

	addr := fmt.Sprintf(":%d", cfg.HTTPPort)
	server := &http.Server{Addr: addr, Handler: mux}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("robot: listening on http://0.0.0.0%s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("robot: shutting down...")
	_ = motorCtrl.Stop(context.Background())
	_ = server.Shutdown(context.Background())
}

func resolveConfigPath() string {
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		return p
	}

	for _, p := range []string{
		"config.yaml",
		"robot/config/config.yaml",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "config.yaml"
}
