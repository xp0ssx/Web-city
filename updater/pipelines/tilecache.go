package pipelines

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tileJob struct {
	Z int
	X int
	Y int
}

func RunTileCache() error {
	apiBase := strings.TrimRight(getStringEnv("TILE_CACHE_API_BASE", "http://api:8080/api/v1"), "/")
	cacheDir := getStringEnv("TILE_CACHE_DIR", "")
	bounds, err := parseTileBounds(getStringEnv("TILE_CACHE_BOUNDS", "36.722,55.097,38.048,56.066"))
	if err != nil {
		return err
	}

	minZoom := getIntEnv("TILE_CACHE_MIN_ZOOM", 9)
	maxZoom := getIntEnv("TILE_CACHE_MAX_ZOOM", 14)
	concurrency := getIntEnv("TILE_CACHE_CONCURRENCY", 4)
	force := getStringEnv("TILE_CACHE_FORCE", "0") == "1"

	if minZoom < 0 || maxZoom > 19 || minZoom > maxZoom {
		return fmt.Errorf("invalid tile cache zoom range: %d..%d", minZoom, maxZoom)
	}
	if concurrency <= 0 {
		return fmt.Errorf("TILE_CACHE_CONCURRENCY must be positive")
	}

	jobs := tileJobs(bounds, minZoom, maxZoom)
	log.Printf(
		"tile cache warmup started: api=%s, bounds=%s, zoom=%d..%d, tiles=%d, concurrency=%d, force=%t",
		apiBase,
		bounds.String(),
		minZoom,
		maxZoom,
		len(jobs),
		concurrency,
		force,
	)

	client := &http.Client{Timeout: 30 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	jobCh := make(chan tileJob)
	errCh := make(chan error, concurrency)

	var mu sync.Mutex
	done := 0
	skipped := 0

	for workerID := 0; workerID < concurrency; workerID++ {
		go func() {
			for job := range jobCh {
				if !force && tileCacheFileExists(cacheDir, job) {
					mu.Lock()
					done++
					skipped++
					if done%500 == 0 || done == len(jobs) {
						log.Printf("tile cache progress: %d/%d, skipped=%d", done, len(jobs), skipped)
					}
					mu.Unlock()
					continue
				}

				if err := requestTile(ctx, client, apiBase, job); err != nil {
					select {
					case errCh <- err:
					default:
					}
					cancel()
					continue
				}

				mu.Lock()
				done++
				if done%500 == 0 || done == len(jobs) {
					log.Printf("tile cache progress: %d/%d, skipped=%d", done, len(jobs), skipped)
				}
				mu.Unlock()
			}
		}()
	}

sendLoop:
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobCh <- job:
		}
	}
	close(jobCh)

	for {
		mu.Lock()
		finished := done
		mu.Unlock()
		if finished >= len(jobs) {
			break
		}

		select {
		case err := <-errCh:
			return err
		case <-time.After(200 * time.Millisecond):
		}
	}

	log.Printf("tile cache warmup completed: tiles=%d, skipped=%d", len(jobs), skipped)
	return nil
}

type tileBounds struct {
	MinLon float64
	MinLat float64
	MaxLon float64
	MaxLat float64
}

func (b tileBounds) String() string {
	return fmt.Sprintf("%.6f,%.6f,%.6f,%.6f", b.MinLon, b.MinLat, b.MaxLon, b.MaxLat)
}

func parseTileBounds(raw string) (tileBounds, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return tileBounds{}, fmt.Errorf("TILE_CACHE_BOUNDS must be min_lon,min_lat,max_lon,max_lat")
	}

	values := make([]float64, 4)
	for i, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return tileBounds{}, fmt.Errorf("invalid TILE_CACHE_BOUNDS value %q: %w", part, err)
		}
		values[i] = value
	}

	bounds := tileBounds{
		MinLon: values[0],
		MinLat: values[1],
		MaxLon: values[2],
		MaxLat: values[3],
	}
	if bounds.MinLon >= bounds.MaxLon || bounds.MinLat >= bounds.MaxLat {
		return tileBounds{}, fmt.Errorf("invalid TILE_CACHE_BOUNDS: %s", bounds.String())
	}
	return bounds, nil
}

func tileJobs(bounds tileBounds, minZoom int, maxZoom int) []tileJob {
	jobs := make([]tileJob, 0)
	for z := minZoom; z <= maxZoom; z++ {
		maxTile := (1 << z) - 1
		minX := clampTile(lonToTileX(bounds.MinLon, z), maxTile)
		maxX := clampTile(lonToTileX(bounds.MaxLon, z), maxTile)
		minY := clampTile(latToTileY(bounds.MaxLat, z), maxTile)
		maxY := clampTile(latToTileY(bounds.MinLat, z), maxTile)

		for x := minX; x <= maxX; x++ {
			for y := minY; y <= maxY; y++ {
				jobs = append(jobs, tileJob{Z: z, X: x, Y: y})
			}
		}
	}
	return jobs
}

func lonToTileX(lon float64, z int) int {
	n := math.Exp2(float64(z))
	return int(math.Floor((lon + 180.0) / 360.0 * n))
}

func latToTileY(lat float64, z int) int {
	latRad := lat * math.Pi / 180.0
	n := math.Exp2(float64(z))
	return int(math.Floor((1.0 - math.Log(math.Tan(latRad)+1.0/math.Cos(latRad))/math.Pi) / 2.0 * n))
}

func clampTile(value int, maxTile int) int {
	if value < 0 {
		return 0
	}
	if value > maxTile {
		return maxTile
	}
	return value
}

func tileCacheFileExists(cacheDir string, job tileJob) bool {
	if cacheDir == "" {
		return false
	}

	path := filepath.Join(cacheDir, "base", strconv.Itoa(job.Z), strconv.Itoa(job.X), strconv.Itoa(job.Y)+".svg")
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func requestTile(ctx context.Context, client *http.Client, apiBase string, job tileJob) error {
	url := fmt.Sprintf("%s/tiles/base/%d/%d/%d", apiBase, job.Z, job.X, job.Y)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "image/svg+xml")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request tile z=%d x=%d y=%d failed: %w", job.Z, job.X, job.Y, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return fmt.Errorf("request tile z=%d x=%d y=%d failed: status=%d body=%s", job.Z, job.X, job.Y, resp.StatusCode, string(body))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
