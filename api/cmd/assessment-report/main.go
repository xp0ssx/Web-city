package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type options struct {
	inputDir string
	output   string
	topN     int
}

type runInfo struct {
	CreatedAt                 string   `json:"created_at"`
	StepM                     float64  `json:"step_m"`
	RadiusM                   float64  `json:"radius_m"`
	PointsTotal               int      `json:"points_total"`
	PointsSuccessful          int      `json:"points_successful"`
	PointsFailed              int      `json:"points_failed"`
	ExcludedAreaSubcategories []string `json:"excluded_area_subcategories"`
}

type scoreRow struct {
	Municipality string
	ID           string
	Title        string
	Points       int
	Average      float64
	Min          float64
	Max          float64
}

type accumulator struct {
	Count int
	Sum   float64
	Min   float64
	Max   float64
}

func main() {
	opts := parseOptions()

	info, err := readRunInfo(filepath.Join(opts.inputDir, "run_info.json"))
	if err != nil {
		exitErr(err)
	}

	generalRows, err := readGeneralSummary(filepath.Join(opts.inputDir, "points.csv"))
	if err != nil {
		exitErr(err)
	}
	profileRows, err := readSummary(filepath.Join(opts.inputDir, "municipality_profile_summary.csv"))
	if err != nil {
		exitErr(err)
	}
	groupRows, err := readSummary(filepath.Join(opts.inputDir, "municipality_group_summary.csv"))
	if err != nil {
		exitErr(err)
	}
	subsectionRows, err := readSubsectionSummary(filepath.Join(opts.inputDir, "subsection_scores.csv"))
	if err != nil {
		exitErr(err)
	}

	if err := writeReport(opts, info, generalRows, profileRows, groupRows, subsectionRows); err != nil {
		exitErr(err)
	}

	fmt.Printf("report written: %s\n", opts.output)
}

func parseOptions() options {
	opts := options{}
	flag.StringVar(&opts.inputDir, "in", "../docs/data", "directory with assessment CSV files")
	flag.StringVar(&opts.output, "out", "../docs/data/assessment_report.md", "report file")
	flag.IntVar(&opts.topN, "top", 5, "number of best and worst municipalities per block")
	flag.Parse()

	if opts.topN <= 0 {
		exitErr(fmt.Errorf("-top must be positive"))
	}
	return opts
}

func readRunInfo(path string) (runInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runInfo{}, err
	}
	var info runInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return runInfo{}, err
	}
	return info, nil
}

func readGeneralSummary(path string) ([]scoreRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}

	header := indexHeader(rows[0])
	municipalityIdx, ok := header["municipality"]
	if !ok {
		return nil, fmt.Errorf("%s: missing municipality column", path)
	}
	scoreIdx, ok := header["general_score"]
	if !ok {
		return nil, fmt.Errorf("%s: missing general_score column", path)
	}
	errorIdx := header["error"]

	acc := make(map[string]*accumulator)
	for _, row := range rows[1:] {
		if errorIdx > 0 && strings.TrimSpace(row[errorIdx]) != "" {
			continue
		}
		municipality := row[municipalityIdx]
		value, err := parseFloat(row[scoreIdx])
		if err != nil {
			return nil, fmt.Errorf("%s: bad general_score: %w", path, err)
		}
		add(acc, municipality, value)
	}

	result := make([]scoreRow, 0, len(acc))
	for municipality, item := range acc {
		result = append(result, scoreRow{
			Municipality: municipality,
			ID:           "general",
			Title:        "Общий профиль",
			Points:       item.Count,
			Average:      item.Sum / float64(item.Count),
			Min:          item.Min,
			Max:          item.Max,
		})
	}
	sortRows(result)
	return result, nil
}

func readSummary(path string) ([]scoreRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}

	header := indexHeader(rows[0])
	required := []string{"municipality", "id", "title", "points_count", "avg_score", "min_score", "max_score"}
	for _, name := range required {
		if _, ok := header[name]; !ok {
			return nil, fmt.Errorf("%s: missing %s column", path, name)
		}
	}

	result := make([]scoreRow, 0, len(rows)-1)
	for _, row := range rows[1:] {
		points, err := strconv.Atoi(row[header["points_count"]])
		if err != nil {
			return nil, fmt.Errorf("%s: bad points_count: %w", path, err)
		}
		avg, err := parseFloat(row[header["avg_score"]])
		if err != nil {
			return nil, fmt.Errorf("%s: bad avg_score: %w", path, err)
		}
		minValue, err := parseFloat(row[header["min_score"]])
		if err != nil {
			return nil, fmt.Errorf("%s: bad min_score: %w", path, err)
		}
		maxValue, err := parseFloat(row[header["max_score"]])
		if err != nil {
			return nil, fmt.Errorf("%s: bad max_score: %w", path, err)
		}
		result = append(result, scoreRow{
			Municipality: row[header["municipality"]],
			ID:           row[header["id"]],
			Title:        row[header["title"]],
			Points:       points,
			Average:      avg,
			Min:          minValue,
			Max:          maxValue,
		})
	}
	sortRows(result)
	return result, nil
}

func readSubsectionSummary(path string) ([]scoreRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s is empty", path)
	}

	header := indexHeader(rows[0])
	required := []string{"municipality", "subsection_id", "subsection_title", "score"}
	for _, name := range required {
		if _, ok := header[name]; !ok {
			return nil, fmt.Errorf("%s: missing %s column", path, name)
		}
	}

	type key struct {
		municipality string
		id           string
		title        string
	}
	acc := make(map[key]*accumulator)
	for _, row := range rows[1:] {
		scoreText := strings.TrimSpace(row[header["score"]])
		if scoreText == "" {
			continue
		}
		value, err := parseFloat(scoreText)
		if err != nil {
			return nil, fmt.Errorf("%s: bad score: %w", path, err)
		}
		k := key{
			municipality: row[header["municipality"]],
			id:           row[header["subsection_id"]],
			title:        row[header["subsection_title"]],
		}
		item := acc[k]
		if item == nil {
			acc[k] = &accumulator{Count: 1, Sum: value, Min: value, Max: value}
			continue
		}
		item.Count++
		item.Sum += value
		item.Min = math.Min(item.Min, value)
		item.Max = math.Max(item.Max, value)
	}

	result := make([]scoreRow, 0, len(acc))
	for k, item := range acc {
		result = append(result, scoreRow{
			Municipality: k.municipality,
			ID:           k.id,
			Title:        k.title,
			Points:       item.Count,
			Average:      item.Sum / float64(item.Count),
			Min:          item.Min,
			Max:          item.Max,
		})
	}
	sortRows(result)
	return result, nil
}

func writeReport(opts options, info runInfo, generalRows []scoreRow, profileRows []scoreRow, groupRows []scoreRow, subsectionRows []scoreRow) error {
	var b strings.Builder

	b.WriteString("Отчёт по сеточной оценке\n\n")
	b.WriteString(fmt.Sprintf("Расчёт выполнен по %d точкам, шаг сетки %.0f м, радиус оценки %.0f м. Ошибок расчёта: %d.\n\n",
		info.PointsSuccessful,
		info.StepM,
		info.RadiusM,
		info.PointsFailed,
	))

	writeBlock(&b, "Общий профиль: лучшие районы", topRows(generalRows, opts.topN))
	writeBlock(&b, "Общий профиль: худшие районы", bottomRows(generalRows, opts.topN))

	writeGroupedBlocks(&b, "Профили", profileRows, opts.topN)
	writeGroupedBlocks(&b, "Группы показателей", groupRows, opts.topN)
	writeGroupedBlocks(&b, "Подкатегории показателей", subsectionRows, opts.topN)

	if err := os.MkdirAll(filepath.Dir(opts.output), 0o755); err != nil {
		return err
	}
	return os.WriteFile(opts.output, []byte(b.String()), 0o644)
}

func writeGroupedBlocks(b *strings.Builder, title string, rows []scoreRow, topN int) {
	b.WriteString(title)
	b.WriteString("\n\n")

	grouped := groupByID(rows)
	ids := make([]string, 0, len(grouped))
	for id := range grouped {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		groupRows := grouped[id]
		if len(groupRows) == 0 {
			continue
		}
		b.WriteString(groupRows[0].Title)
		b.WriteString("\n")
		writeCompactList(b, "лучшие", topRows(groupRows, topN))
		writeCompactList(b, "худшие", bottomRows(groupRows, topN))
		b.WriteString("\n")
	}
}

func writeBlock(b *strings.Builder, title string, rows []scoreRow) {
	b.WriteString(title)
	b.WriteString("\n")
	for i, row := range rows {
		b.WriteString(fmt.Sprintf("%d. %s — %.2f (%d точек)\n", i+1, row.Municipality, row.Average, row.Points))
	}
	b.WriteString("\n")
}

func writeCompactList(b *strings.Builder, title string, rows []scoreRow) {
	items := make([]string, 0, len(rows))
	for _, row := range rows {
		items = append(items, fmt.Sprintf("%s — %.2f", row.Municipality, row.Average))
	}
	b.WriteString(title)
	b.WriteString(": ")
	b.WriteString(strings.Join(items, "; "))
	b.WriteString("\n")
}

func groupByID(rows []scoreRow) map[string][]scoreRow {
	result := make(map[string][]scoreRow)
	for _, row := range rows {
		result[row.ID] = append(result[row.ID], row)
	}
	for id := range result {
		sortRows(result[id])
	}
	return result
}

func topRows(rows []scoreRow, limit int) []scoreRow {
	copied := append([]scoreRow(nil), rows...)
	sortRows(copied)
	if len(copied) < limit {
		return copied
	}
	return copied[:limit]
}

func bottomRows(rows []scoreRow, limit int) []scoreRow {
	copied := append([]scoreRow(nil), rows...)
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Average != copied[j].Average {
			return copied[i].Average < copied[j].Average
		}
		return copied[i].Municipality < copied[j].Municipality
	})
	if len(copied) < limit {
		return copied
	}
	return copied[:limit]
}

func sortRows(rows []scoreRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Average != rows[j].Average {
			return rows[i].Average > rows[j].Average
		}
		return rows[i].Municipality < rows[j].Municipality
	})
}

func add(acc map[string]*accumulator, key string, value float64) {
	item := acc[key]
	if item == nil {
		acc[key] = &accumulator{Count: 1, Sum: value, Min: value, Max: value}
		return
	}
	item.Count++
	item.Sum += value
	item.Min = math.Min(item.Min, value)
	item.Max = math.Max(item.Max, value)
}

func indexHeader(header []string) map[string]int {
	result := make(map[string]int, len(header))
	for i, name := range header {
		result[name] = i
	}
	return result
}

func parseFloat(value string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(value), 64)
}

func exitErr(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
