package main

import (
	"log"
	"os"
	"os/exec"
	"strings"

	"web-city/updater/pipelines"
)

const allowedModes = "osm-raw | osm | map-layers | tile-cache | datamos | static | all"

func main() {
	mode := strings.ToLower(strings.TrimSpace(getEnv("UPDATER_MODE", "")))

	if mode == "" {
		log.Fatalf("UPDATER_MODE is required (allowed: %s)", allowedModes)
	}

	switch mode {
	case "osm-raw":
		runOSMRaw()

	case "osm":
		runOSM()

	case "map-layers":
		runMapLayers()

	case "tile-cache":
		runTileCache()

	case "datamos":
		runDataMos()

	case "static":
		runStatic()

	case "all":
		runOSM()
		runMapLayers()
		runDataMos()
		runStatic()

	default:
		log.Fatalf("invalid UPDATER_MODE=%s (allowed: %s)", mode, allowedModes)
	}
}

func runOSMRaw() {
	script := getEnv("OSM_RAW_IMPORT_SCRIPT", "/app/run-import.sh")
	log.Printf("starting raw OSM import: %s", script)

	cmd := exec.Command(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		log.Fatalf("raw OSM import failed: %v", err)
	}
	log.Println("raw OSM import done")
}

func runOSM() {
	log.Println("starting OSM pipeline")
	if err := pipelines.RunOSM(); err != nil {
		log.Fatalf("OSM failed: %v", err)
	}
	log.Println("OSM done")
}

func runMapLayers() {
	log.Println("starting map layers pipeline")
	if err := pipelines.RunMapLayers(); err != nil {
		log.Fatalf("map layers failed: %v", err)
	}
	log.Println("map layers done")
}

func runTileCache() {
	log.Println("starting tile cache pipeline")
	if err := pipelines.RunTileCache(); err != nil {
		log.Fatalf("tile cache failed: %v", err)
	}
	log.Println("tile cache done")
}

func runDataMos() {
	log.Println("starting DataMos pipeline")
	if err := pipelines.RunDataMos(); err != nil {
		log.Fatalf("DataMos failed: %v", err)
	}
	log.Println("DataMos done")
}

func runStatic() {
	log.Println("starting static data pipeline")
	if err := pipelines.RunStatic(); err != nil {
		log.Fatalf("static data failed: %v", err)
	}
	log.Println("static data done")
}

func getEnv(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
