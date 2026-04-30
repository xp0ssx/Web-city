package main

import (
	"log"
	"os"
	"strings"

	"web-city/updater/pipelines"
)

func main() {
	mode := strings.ToLower(strings.TrimSpace(getEnv("UPDATER_MODE", "")))

	if mode == "" {
		log.Fatalf("UPDATER_MODE is required (allowed: osm | datamos | all)")
	}

	switch mode {
	case "osm":
		runOSM()

	case "datamos":
		runDataMos()

	case "all":
		runOSM()
		runDataMos()

	default:
		log.Fatalf("invalid UPDATER_MODE=%s (allowed: osm | datamos | all)", mode)
	}
}

func runOSM() {
	log.Println("starting OSM pipeline")
	if err := pipelines.RunOSM(); err != nil {
		log.Fatalf("OSM failed: %v", err)
	}
	log.Println("OSM done")
}

func runDataMos() {
	log.Println("starting DataMos pipeline")
	if err := pipelines.RunDataMos(); err != nil {
		log.Fatalf("DataMos failed: %v", err)
	}
	log.Println("DataMos done")
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
