package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultDSN    = "postgres://webcity:webcity@localhost:5432/webcity?sslmode=disable"
	defaultAPIURL = "http://localhost:8090/api/v1"

	generalProfile = "general"
)

var excludedAreaSubcategories = []string{
	"water_bodies",
	"parks_greenery",
	"natural_greenery",
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	*s = append(*s, value)
	return nil
}

type options struct {
	dsn                      string
	apiBaseURL               string
	outDir                   string
	stepM                    float64
	radiusM                  float64
	excludeBufferM           float64
	workers                  int
	topN                     int
	maxPointsPerMunicipality int
	municipalities           []string
	timeout                  time.Duration
}

type gridPoint struct {
	Municipality string
	ParentName   string
	PointIndex   int
	Lon          float64
	Lat          float64
}

type evaluationRequest struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Profile string  `json:"profile"`
	RadiusM float64 `json:"radius_m"`
}

type evaluationResult struct {
	Score         *float64             `json:"score"`
	Profile       string               `json:"profile"`
	ProfileTitle  string               `json:"profile_title"`
	WeightsMode   string               `json:"weights_mode"`
	Lat           float64              `json:"lat"`
	Lon           float64              `json:"lon"`
	RadiusM       float64              `json:"radius_m"`
	Municipality  *municipalityContext `json:"municipality,omitempty"`
	ProfileScores []profileScore       `json:"profile_scores,omitempty"`
	Groups        []groupResult        `json:"groups"`
}

type municipalityContext struct {
	Name       string `json:"name"`
	ParentName string `json:"parent_name,omitempty"`
}

type profileScore struct {
	Profile      string   `json:"profile"`
	ProfileTitle string   `json:"profile_title"`
	Weight       float64  `json:"weight"`
	Score        *float64 `json:"score"`
}

type groupResult struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Score    *float64        `json:"score"`
	Sections []sectionResult `json:"sections"`
}

type sectionResult struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Score       *float64           `json:"score"`
	Subsections []subsectionResult `json:"subsections"`
}

type subsectionResult struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Score *float64 `json:"score"`
}

type pointEvaluation struct {
	Point gridPoint
	Eval  evaluationResult
	Err   error
}

type summaryKey struct {
	Municipality string
	ID           string
	Title        string
}

type summaryAccumulator struct {
	Count int
	Sum   float64
	Min   float64
	Max   float64
}

type summaryRow struct {
	Municipality string
	ID           string
	Title        string
	Count        int
	Average      float64
	Min          float64
	Max          float64
}

func main() {
	opts := parseOptions()

	ctx := context.Background()
	db, err := pgxpool.New(ctx, opts.dsn)
	if err != nil {
		log.Fatalf("db pool init failed: %v", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatalf("db ping failed: %v", err)
	}

	points, err := loadGridPoints(ctx, db, opts)
	if err != nil {
		log.Fatalf("load grid points failed: %v", err)
	}
	if len(points) == 0 {
		log.Fatal("grid has no points after area exclusions")
	}

	log.Printf("assessment grid: points=%d, step=%.0fm, radius=%.0fm, workers=%d", len(points), opts.stepM, opts.radiusM, opts.workers)

	results := evaluatePoints(ctx, opts, points)

	if err := os.MkdirAll(opts.outDir, 0o755); err != nil {
		log.Fatalf("create output dir failed: %v", err)
	}
	if err := writeOutputs(opts, results); err != nil {
		log.Fatalf("write outputs failed: %v", err)
	}

	var failed int
	for _, item := range results {
		if item.Err != nil {
			failed++
		}
	}
	log.Printf("assessment grid done: successful=%d, failed=%d, out=%s", len(results)-failed, failed, opts.outDir)
}

func parseOptions() options {
	var municipalities stringList
	timeoutSeconds := flag.Int("timeout", 60, "HTTP request timeout in seconds")
	opts := options{}

	flag.StringVar(&opts.dsn, "dsn", envOrDefault("ANALYTICS_DATABASE_DSN", defaultDSN), "PostgreSQL DSN")
	flag.StringVar(&opts.apiBaseURL, "api", defaultAPIURL, "API base URL, for example http://localhost:8090/api/v1")
	flag.StringVar(&opts.outDir, "out", "../data/assessment_grid", "output directory")
	flag.Float64Var(&opts.stepM, "step", 1000, "grid step in meters")
	flag.Float64Var(&opts.radiusM, "radius", 1000, "assessment radius in meters")
	flag.Float64Var(&opts.excludeBufferM, "exclude-buffer", 0, "additional buffer around excluded areas in meters")
	flag.IntVar(&opts.workers, "workers", 2, "parallel API requests")
	flag.IntVar(&opts.topN, "top", 10, "number of best municipalities per group")
	flag.IntVar(&opts.maxPointsPerMunicipality, "max-points-per-municipality", 0, "debug limit per municipality, 0 means no limit")
	flag.Var(&municipalities, "municipality", "municipality name to include; can be used multiple times")
	flag.Parse()

	opts.municipalities = municipalities
	if opts.municipalities == nil {
		opts.municipalities = []string{}
	}
	opts.timeout = time.Duration(*timeoutSeconds) * time.Second
	opts.apiBaseURL = strings.TrimRight(opts.apiBaseURL, "/")

	if opts.stepM <= 0 {
		log.Fatal("-step must be positive")
	}
	if opts.radiusM <= 0 {
		log.Fatal("-radius must be positive")
	}
	if opts.excludeBufferM < 0 {
		log.Fatal("-exclude-buffer must be zero or positive")
	}
	if opts.workers <= 0 {
		log.Fatal("-workers must be positive")
	}
	if opts.topN <= 0 {
		log.Fatal("-top must be positive")
	}
	if opts.timeout <= 0 {
		log.Fatal("-timeout must be positive")
	}

	return opts
}

func loadGridPoints(ctx context.Context, db *pgxpool.Pool, opts options) ([]gridPoint, error) {
	rows, err := db.Query(ctx, `
		WITH selected_municipalities AS (
			SELECT
				name,
				COALESCE(parent_name, '') AS parent_name,
				geom,
				ST_Transform(geom, 32637) AS geom_m
			FROM administrative_areas
			WHERE area_type = 'municipality'
				AND (cardinality($1::text[]) = 0 OR name = ANY($1::text[]))
		),
		raw_points AS (
			SELECT
				m.name AS municipality,
				m.parent_name,
				p.geom_4326 AS geom
			FROM selected_municipalities m
			CROSS JOIN LATERAL (
				SELECT
					ST_PointOnSurface(cell.geom) AS geom_m,
					ST_Transform(ST_PointOnSurface(cell.geom), 4326) AS geom_4326
				FROM ST_SquareGrid($4::float8, m.geom_m) AS cell
			) p
			WHERE ST_Covers(m.geom_m, p.geom_m)
				AND NOT EXISTS (
					SELECT 1
					FROM infrastructure_areas a
					WHERE a.subcategory = ANY($2::text[])
						AND (
							($3::float8 <= 0 AND a.geom && p.geom_4326 AND ST_Covers(a.geom, p.geom_4326))
							OR ($3::float8 > 0 AND ST_DWithin(ST_Transform(a.geom, 32637), p.geom_m, $3::float8))
						)
				)
		),
		numbered AS (
			SELECT
				municipality,
				parent_name,
				ROW_NUMBER() OVER (PARTITION BY municipality ORDER BY ST_Y(geom), ST_X(geom))::int AS point_index,
				ST_X(geom)::float8 AS lon,
				ST_Y(geom)::float8 AS lat
			FROM raw_points
		)
		SELECT municipality, parent_name, point_index, lon, lat
		FROM numbered
		WHERE $5::int <= 0 OR point_index <= $5::int
		ORDER BY municipality, point_index
	`,
		opts.municipalities,
		excludedAreaSubcategories,
		opts.excludeBufferM,
		opts.stepM,
		opts.maxPointsPerMunicipality,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	points := make([]gridPoint, 0)
	for rows.Next() {
		var point gridPoint
		if err := rows.Scan(&point.Municipality, &point.ParentName, &point.PointIndex, &point.Lon, &point.Lat); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return points, nil
}

func evaluatePoints(ctx context.Context, opts options, points []gridPoint) []pointEvaluation {
	jobs := make(chan gridPoint)
	results := make(chan pointEvaluation)

	var wg sync.WaitGroup
	client := &http.Client{Timeout: opts.timeout}

	for i := 0; i < opts.workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for point := range jobs {
				eval, err := evaluatePoint(ctx, client, opts, point)
				results <- pointEvaluation{Point: point, Eval: eval, Err: err}
			}
		}(i)
	}

	go func() {
		defer close(jobs)
		for _, point := range points {
			jobs <- point
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	collected := make([]pointEvaluation, 0, len(points))
	processed := 0
	for result := range results {
		collected = append(collected, result)
		processed++
		if processed%100 == 0 || processed == len(points) {
			log.Printf("assessment progress: %d/%d", processed, len(points))
		}
	}

	sort.Slice(collected, func(i, j int) bool {
		if collected[i].Point.Municipality != collected[j].Point.Municipality {
			return collected[i].Point.Municipality < collected[j].Point.Municipality
		}
		return collected[i].Point.PointIndex < collected[j].Point.PointIndex
	})

	return collected
}

func evaluatePoint(ctx context.Context, client *http.Client, opts options, point gridPoint) (evaluationResult, error) {
	payload := evaluationRequest{
		Lat:     point.Lat,
		Lon:     point.Lon,
		Profile: generalProfile,
		RadiusM: opts.radiusM,
	}

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		result, err := postEvaluation(ctx, client, opts.apiBaseURL, payload)
		if err == nil {
			return result, nil
		}
		lastErr = err
		time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
	}

	return evaluationResult{}, lastErr
}

func postEvaluation(ctx context.Context, client *http.Client, apiBaseURL string, payload evaluationRequest) (evaluationResult, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return evaluationResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBaseURL+"/assessments/evaluate", bytes.NewReader(body))
	if err != nil {
		return evaluationResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return evaluationResult{}, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return evaluationResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return evaluationResult{}, fmt.Errorf("API status %d: %s", resp.StatusCode, trimForLog(respBody, 500))
	}

	var result evaluationResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return evaluationResult{}, fmt.Errorf("decode API response failed: %w: %s", err, trimForLog(respBody, 500))
	}

	return result, nil
}

func writeOutputs(opts options, results []pointEvaluation) error {
	if err := writePointsCSV(filepath.Join(opts.outDir, "points.csv"), results); err != nil {
		return err
	}
	if err := writeProfileScoresCSV(filepath.Join(opts.outDir, "profile_scores.csv"), results); err != nil {
		return err
	}
	if err := writeGroupScoresCSV(filepath.Join(opts.outDir, "group_scores.csv"), results); err != nil {
		return err
	}
	if err := writeSectionScoresCSV(filepath.Join(opts.outDir, "section_scores.csv"), results); err != nil {
		return err
	}
	if err := writeSubsectionScoresCSV(filepath.Join(opts.outDir, "subsection_scores.csv"), results); err != nil {
		return err
	}
	if err := writeSummaryCSV(filepath.Join(opts.outDir, "municipality_profile_summary.csv"), collectProfileSummary(results)); err != nil {
		return err
	}
	groupSummary := collectGroupSummary(results)
	if err := writeSummaryCSV(filepath.Join(opts.outDir, "municipality_group_summary.csv"), groupSummary); err != nil {
		return err
	}
	if err := writeBestByGroupCSV(filepath.Join(opts.outDir, "best_municipalities_by_group.csv"), groupSummary, opts.topN); err != nil {
		return err
	}
	if err := writeRunInfo(filepath.Join(opts.outDir, "run_info.json"), opts, results); err != nil {
		return err
	}
	return nil
}

func writePointsCSV(path string, results []pointEvaluation) error {
	return writeCSV(path, []string{
		"municipality",
		"parent_name",
		"point_index",
		"lon",
		"lat",
		"general_score",
		"api_municipality",
		"error",
	}, func(w *csv.Writer) error {
		for _, item := range results {
			apiMunicipality := ""
			if item.Eval.Municipality != nil {
				apiMunicipality = item.Eval.Municipality.Name
			}
			row := []string{
				item.Point.Municipality,
				item.Point.ParentName,
				strconv.Itoa(item.Point.PointIndex),
				formatFloat(item.Point.Lon),
				formatFloat(item.Point.Lat),
				formatScore(item.Eval.Score),
				apiMunicipality,
				errorString(item.Err),
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeProfileScoresCSV(path string, results []pointEvaluation) error {
	return writeCSV(path, []string{
		"municipality",
		"parent_name",
		"point_index",
		"profile",
		"profile_title",
		"profile_weight",
		"score",
	}, func(w *csv.Writer) error {
		for _, item := range results {
			if item.Err != nil {
				continue
			}
			for _, profile := range item.Eval.ProfileScores {
				row := []string{
					item.Point.Municipality,
					item.Point.ParentName,
					strconv.Itoa(item.Point.PointIndex),
					profile.Profile,
					profile.ProfileTitle,
					formatFloat(profile.Weight),
					formatScore(profile.Score),
				}
				if err := w.Write(row); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func writeGroupScoresCSV(path string, results []pointEvaluation) error {
	return writeCSV(path, []string{
		"municipality",
		"parent_name",
		"point_index",
		"group_id",
		"group_title",
		"score",
	}, func(w *csv.Writer) error {
		for _, item := range results {
			if item.Err != nil {
				continue
			}
			for _, group := range item.Eval.Groups {
				row := []string{
					item.Point.Municipality,
					item.Point.ParentName,
					strconv.Itoa(item.Point.PointIndex),
					group.ID,
					group.Title,
					formatScorePercent(group.Score),
				}
				if err := w.Write(row); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func writeSectionScoresCSV(path string, results []pointEvaluation) error {
	return writeCSV(path, []string{
		"municipality",
		"parent_name",
		"point_index",
		"group_id",
		"group_title",
		"section_id",
		"section_title",
		"score",
	}, func(w *csv.Writer) error {
		for _, item := range results {
			if item.Err != nil {
				continue
			}
			for _, group := range item.Eval.Groups {
				for _, section := range group.Sections {
					row := []string{
						item.Point.Municipality,
						item.Point.ParentName,
						strconv.Itoa(item.Point.PointIndex),
						group.ID,
						group.Title,
						section.ID,
						section.Title,
						formatScorePercent(section.Score),
					}
					if err := w.Write(row); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func writeSubsectionScoresCSV(path string, results []pointEvaluation) error {
	return writeCSV(path, []string{
		"municipality",
		"parent_name",
		"point_index",
		"group_id",
		"group_title",
		"section_id",
		"section_title",
		"subsection_id",
		"subsection_title",
		"score",
	}, func(w *csv.Writer) error {
		for _, item := range results {
			if item.Err != nil {
				continue
			}
			for _, group := range item.Eval.Groups {
				for _, section := range group.Sections {
					for _, subsection := range section.Subsections {
						row := []string{
							item.Point.Municipality,
							item.Point.ParentName,
							strconv.Itoa(item.Point.PointIndex),
							group.ID,
							group.Title,
							section.ID,
							section.Title,
							subsection.ID,
							subsection.Title,
							formatScorePercent(subsection.Score),
						}
						if err := w.Write(row); err != nil {
							return err
						}
					}
				}
			}
		}
		return nil
	})
}

func collectProfileSummary(results []pointEvaluation) []summaryRow {
	acc := make(map[summaryKey]*summaryAccumulator)
	for _, item := range results {
		if item.Err != nil {
			continue
		}
		for _, profile := range item.Eval.ProfileScores {
			addSummaryValue(acc, summaryKey{
				Municipality: item.Point.Municipality,
				ID:           profile.Profile,
				Title:        profile.ProfileTitle,
			}, scorePointer(profile.Score))
		}
	}
	return summaryRows(acc)
}

func collectGroupSummary(results []pointEvaluation) []summaryRow {
	acc := make(map[summaryKey]*summaryAccumulator)
	for _, item := range results {
		if item.Err != nil {
			continue
		}
		for _, group := range item.Eval.Groups {
			addSummaryValue(acc, summaryKey{
				Municipality: item.Point.Municipality,
				ID:           group.ID,
				Title:        group.Title,
			}, scorePointerPercent(group.Score))
		}
	}
	return summaryRows(acc)
}

func addSummaryValue(acc map[summaryKey]*summaryAccumulator, key summaryKey, value *float64) {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return
	}
	item := acc[key]
	if item == nil {
		acc[key] = &summaryAccumulator{
			Count: 1,
			Sum:   *value,
			Min:   *value,
			Max:   *value,
		}
		return
	}
	item.Count++
	item.Sum += *value
	if *value < item.Min {
		item.Min = *value
	}
	if *value > item.Max {
		item.Max = *value
	}
}

func summaryRows(acc map[summaryKey]*summaryAccumulator) []summaryRow {
	rows := make([]summaryRow, 0, len(acc))
	for key, item := range acc {
		if item.Count == 0 {
			continue
		}
		rows = append(rows, summaryRow{
			Municipality: key.Municipality,
			ID:           key.ID,
			Title:        key.Title,
			Count:        item.Count,
			Average:      item.Sum / float64(item.Count),
			Min:          item.Min,
			Max:          item.Max,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ID != rows[j].ID {
			return rows[i].ID < rows[j].ID
		}
		if rows[i].Average != rows[j].Average {
			return rows[i].Average > rows[j].Average
		}
		return rows[i].Municipality < rows[j].Municipality
	})
	return rows
}

func writeSummaryCSV(path string, rows []summaryRow) error {
	return writeCSV(path, []string{
		"municipality",
		"id",
		"title",
		"points_count",
		"avg_score",
		"min_score",
		"max_score",
	}, func(w *csv.Writer) error {
		for _, row := range rows {
			if err := w.Write([]string{
				row.Municipality,
				row.ID,
				row.Title,
				strconv.Itoa(row.Count),
				formatFloat(row.Average),
				formatFloat(row.Min),
				formatFloat(row.Max),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeBestByGroupCSV(path string, rows []summaryRow, topN int) error {
	byGroup := make(map[string][]summaryRow)
	for _, row := range rows {
		byGroup[row.ID] = append(byGroup[row.ID], row)
	}

	groupIDs := make([]string, 0, len(byGroup))
	for groupID := range byGroup {
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)

	return writeCSV(path, []string{
		"group_id",
		"group_title",
		"rank",
		"municipality",
		"points_count",
		"avg_score",
		"min_score",
		"max_score",
	}, func(w *csv.Writer) error {
		for _, groupID := range groupIDs {
			groupRows := byGroup[groupID]
			sort.Slice(groupRows, func(i, j int) bool {
				if groupRows[i].Average != groupRows[j].Average {
					return groupRows[i].Average > groupRows[j].Average
				}
				return groupRows[i].Municipality < groupRows[j].Municipality
			})
			limit := topN
			if len(groupRows) < limit {
				limit = len(groupRows)
			}
			for i := 0; i < limit; i++ {
				row := groupRows[i]
				if err := w.Write([]string{
					row.ID,
					row.Title,
					strconv.Itoa(i + 1),
					row.Municipality,
					strconv.Itoa(row.Count),
					formatFloat(row.Average),
					formatFloat(row.Min),
					formatFloat(row.Max),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func writeRunInfo(path string, opts options, results []pointEvaluation) error {
	var failed int
	for _, item := range results {
		if item.Err != nil {
			failed++
		}
	}

	payload := map[string]any{
		"created_at":                  time.Now().Format(time.RFC3339),
		"api_base_url":                opts.apiBaseURL,
		"step_m":                      opts.stepM,
		"radius_m":                    opts.radiusM,
		"exclude_buffer_m":            opts.excludeBufferM,
		"excluded_area_subcategories": excludedAreaSubcategories,
		"municipalities":              opts.municipalities,
		"max_points_per_municipality": opts.maxPointsPerMunicipality,
		"workers":                     opts.workers,
		"top":                         opts.topN,
		"points_total":                len(results),
		"points_successful":           len(results) - failed,
		"points_failed":               failed,
		"generated_files":             []string{"points.csv", "profile_scores.csv", "group_scores.csv", "section_scores.csv", "subsection_scores.csv", "municipality_profile_summary.csv", "municipality_group_summary.csv", "best_municipalities_by_group.csv"},
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeCSV(path string, header []string, writeRows func(*csv.Writer) error) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write(header); err != nil {
		return err
	}
	if err := writeRows(writer); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	return nil
}

func scorePointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func scorePointerPercent(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copied := *value * 100
	return &copied
}

func formatScore(value *float64) string {
	if value == nil {
		return ""
	}
	return formatFloat(*value)
}

func formatScorePercent(value *float64) string {
	if value == nil {
		return ""
	}
	return formatFloat(*value * 100)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func trimForLog(data []byte, limit int) string {
	text := string(data)
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}
