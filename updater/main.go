package main

import (
	"log"
	"os"
	"os/exec"
	"strings"
)

func main() {
	mode := strings.ToLower(strings.TrimSpace(getEnv("UPDATER_MODE", "all")))
	log.Printf("updater mode: %s", mode)

	switch mode {
	case "osm":
		runOSM()
	case "datamos":
		runDataMos()
	case "all":
		runOSM()
		runDataMos()
	default:
		log.Fatalf("unsupported UPDATER_MODE: %s (allowed: osm|datamos|all)", mode)
	}
}

func runOSM() {
	log.Println("starting OSM pipeline via /app/run-import.sh")
	cmd := exec.Command("/app/run-import.sh")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("OSM pipeline failed: %v", err)
	}
	log.Println("OSM pipeline completed")
}

func runDataMos() {
	log.Println("starting DataMos pipeline")
	if err := RunDataMos(); err != nil {
		log.Fatalf("DataMos pipeline failed: %v", err)
	}
	log.Println("DataMos pipeline completed")
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}