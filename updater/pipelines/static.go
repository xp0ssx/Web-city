package pipelines

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	staticAdminSource    = "moscow_admin_geojson"
	staticEconomicSource = "irn"
)

type staticGeoJSONCollection struct {
	Features []staticGeoJSONFeature `json:"features"`
}

type staticGeoJSONFeature struct {
	Geometry   json.RawMessage `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

type staticAdministrativeArea struct {
	AreaType     string
	Name         string
	ParentName   string
	Abbreviation string
	OKATO        string
	OKTMO        string
	TypeMO       string
	SourceFile   string
	GeometryJSON string
}

type staticEconomicMetric struct {
	PeriodMonth        time.Time
	MunicipalityName   string
	SourceDistrict     string
	PriceRubM2         int
	YieldVsBankDeposit float64
	SourceFile         string
}

func RunStatic() error {
	dataDir := getStringEnv("STATIC_DATA_DIR", "../data")
	aoFile := getStringEnv("STATIC_AO_GEOJSON", "ao.geojson")
	moFile := getStringEnv("STATIC_MO_GEOJSON", "mo.geojson")
	economicFile := getStringEnv("STATIC_ECONOMIC_CSV", "irn_moscow_district_metrics_2026_03.csv")

	periodRaw := getStringEnv("STATIC_ECONOMIC_PERIOD", "2026-03-01")
	periodMonth, err := time.Parse("2006-01-02", periodRaw)
	if err != nil {
		return fmt.Errorf("STATIC_ECONOMIC_PERIOD must use YYYY-MM-DD format: %w", err)
	}

	adminAreas, err := loadStaticAdministrativeAreas(dataDir, aoFile, moFile)
	if err != nil {
		return err
	}

	economicMetrics, err := loadStaticEconomicMetrics(dataDir, economicFile, periodMonth)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	db, err := pgxpool.New(ctx, databaseDSN())
	if err != nil {
		return fmt.Errorf("db pool init failed: %w", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed: %w", err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db transaction begin failed: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM administrative_areas WHERE source = $1`, staticAdminSource); err != nil {
		return fmt.Errorf("delete old administrative areas failed: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM district_economic_metrics WHERE source = $1 AND period_month = $2`, staticEconomicSource, periodMonth); err != nil {
		return fmt.Errorf("delete old economic metrics failed: %w", err)
	}

	adminRows, err := saveStaticAdministrativeAreas(ctx, tx, adminAreas)
	if err != nil {
		return err
	}

	economicRows, err := saveStaticEconomicMetrics(ctx, tx, economicMetrics)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db transaction commit failed: %w", err)
	}

	log.Printf("static data imported: administrative_areas=%d, economic_metrics=%d", adminRows, economicRows)
	return nil
}

func loadStaticAdministrativeAreas(dataDir string, aoFile string, moFile string) ([]staticAdministrativeArea, error) {
	areas := make([]staticAdministrativeArea, 0, 160)

	aoAreas, err := loadStaticAdminGeoJSON(
		staticDataPath(dataDir, aoFile),
		aoFile,
		func(feature staticGeoJSONFeature) (staticAdministrativeArea, error) {
			name, err := requiredStringProperty(feature.Properties, "NAME")
			if err != nil {
				return staticAdministrativeArea{}, err
			}

			return staticAdministrativeArea{
				AreaType:     "admin_district",
				Name:         name,
				Abbreviation: stringProperty(feature.Properties, "ABBREV"),
				OKATO:        stringProperty(feature.Properties, "OKATO"),
				SourceFile:   aoFile,
				GeometryJSON: string(feature.Geometry),
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	areas = append(areas, aoAreas...)

	moAreas, err := loadStaticAdminGeoJSON(
		staticDataPath(dataDir, moFile),
		moFile,
		func(feature staticGeoJSONFeature) (staticAdministrativeArea, error) {
			name, err := requiredStringProperty(feature.Properties, "NAME")
			if err != nil {
				return staticAdministrativeArea{}, err
			}

			return staticAdministrativeArea{
				AreaType:     "municipality",
				Name:         name,
				ParentName:   stringProperty(feature.Properties, "NAME_AO"),
				OKATO:        stringProperty(feature.Properties, "OKATO"),
				OKTMO:        stringProperty(feature.Properties, "OKTMO"),
				TypeMO:       stringProperty(feature.Properties, "TYPE_MO"),
				SourceFile:   moFile,
				GeometryJSON: string(feature.Geometry),
			}, nil
		},
	)
	if err != nil {
		return nil, err
	}
	areas = append(areas, moAreas...)

	return areas, nil
}

func loadStaticAdminGeoJSON(path string, sourceFile string, mapper func(staticGeoJSONFeature) (staticAdministrativeArea, error)) ([]staticAdministrativeArea, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s failed: %w", path, err)
	}

	var collection staticGeoJSONCollection
	if err := json.Unmarshal(data, &collection); err != nil {
		return nil, fmt.Errorf("decode %s failed: %w", path, err)
	}

	areas := make([]staticAdministrativeArea, 0, len(collection.Features))
	for i, feature := range collection.Features {
		if len(feature.Geometry) == 0 {
			return nil, fmt.Errorf("%s feature %d has empty geometry", sourceFile, i)
		}

		area, err := mapper(feature)
		if err != nil {
			return nil, fmt.Errorf("%s feature %d: %w", sourceFile, i, err)
		}
		areas = append(areas, area)
	}

	log.Printf("static geojson loaded: file=%s, features=%d", sourceFile, len(areas))
	return areas, nil
}

func loadStaticEconomicMetrics(dataDir string, fileName string, periodMonth time.Time) ([]staticEconomicMetric, error) {
	path := staticDataPath(dataDir, fileName)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s failed: %w", path, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read %s header failed: %w", path, err)
	}

	columns := csvColumns(header)
	requiredColumns := []string{"municipality", "price_rub_m2", "yield_vs_bank_deposit", "source_district"}
	for _, column := range requiredColumns {
		if _, ok := columns[column]; !ok {
			return nil, fmt.Errorf("%s is missing required column %s", fileName, column)
		}
	}

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read %s rows failed: %w", path, err)
	}

	metrics := make([]staticEconomicMetric, 0, len(records))
	for i, record := range records {
		rowNumber := i + 2
		metric, err := parseStaticEconomicMetric(fileName, columns, record, periodMonth)
		if err != nil {
			return nil, fmt.Errorf("%s row %d: %w", fileName, rowNumber, err)
		}
		metrics = append(metrics, metric)
	}

	log.Printf("static economic csv loaded: file=%s, rows=%d", fileName, len(metrics))
	return metrics, nil
}

func parseStaticEconomicMetric(fileName string, columns map[string]int, record []string, periodMonth time.Time) (staticEconomicMetric, error) {
	metric := staticEconomicMetric{
		PeriodMonth:      periodMonth,
		MunicipalityName: csvValue(record, columns, "municipality"),
		SourceDistrict:   csvValue(record, columns, "source_district"),
		SourceFile:       fileName,
	}

	if metric.MunicipalityName == "" {
		return staticEconomicMetric{}, fmt.Errorf("municipality is empty")
	}
	if metric.SourceDistrict == "" {
		return staticEconomicMetric{}, fmt.Errorf("source_district is empty")
	}

	price, err := strconv.Atoi(csvValue(record, columns, "price_rub_m2"))
	if err != nil {
		return staticEconomicMetric{}, fmt.Errorf("bad price_rub_m2: %w", err)
	}
	metric.PriceRubM2 = price

	yield, err := strconv.ParseFloat(csvValue(record, columns, "yield_vs_bank_deposit"), 64)
	if err != nil {
		return staticEconomicMetric{}, fmt.Errorf("bad yield_vs_bank_deposit: %w", err)
	}
	metric.YieldVsBankDeposit = yield

	return metric, nil
}

func saveStaticAdministrativeAreas(ctx context.Context, tx pgxExecutor, areas []staticAdministrativeArea) (int64, error) {
	total := int64(0)
	for _, area := range areas {
		tag, err := tx.Exec(ctx, `
			INSERT INTO administrative_areas (
				area_type,
				name,
				parent_name,
				abbreviation,
				okato,
				oktmo,
				type_mo,
				source,
				source_file,
				geom,
				updated_at
			)
			VALUES (
				$1,
				$2,
				NULLIF($3, ''),
				NULLIF($4, ''),
				NULLIF($5, ''),
				NULLIF($6, ''),
				NULLIF($7, ''),
				$8,
				$9,
				ST_Multi(ST_CollectionExtract(ST_MakeValid(ST_SetSRID(ST_GeomFromGeoJSON($10), 4326)), 3)),
				now()
			)
			ON CONFLICT (area_type, name)
			DO UPDATE SET
				parent_name = EXCLUDED.parent_name,
				abbreviation = EXCLUDED.abbreviation,
				okato = EXCLUDED.okato,
				oktmo = EXCLUDED.oktmo,
				type_mo = EXCLUDED.type_mo,
				source = EXCLUDED.source,
				source_file = EXCLUDED.source_file,
				geom = EXCLUDED.geom,
				updated_at = now()
		`,
			area.AreaType,
			area.Name,
			area.ParentName,
			area.Abbreviation,
			area.OKATO,
			area.OKTMO,
			area.TypeMO,
			staticAdminSource,
			area.SourceFile,
			area.GeometryJSON,
		)
		if err != nil {
			return 0, fmt.Errorf("save administrative area %s/%s failed: %w", area.AreaType, area.Name, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

func saveStaticEconomicMetrics(ctx context.Context, tx pgxExecutor, metrics []staticEconomicMetric) (int64, error) {
	total := int64(0)
	for _, metric := range metrics {
		tag, err := tx.Exec(ctx, `
			INSERT INTO district_economic_metrics (
				period_month,
				municipality_name,
				source_district,
				price_rub_m2,
				yield_vs_bank_deposit,
				source,
				source_file,
				updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7, now())
			ON CONFLICT (period_month, municipality_name)
			DO UPDATE SET
				source_district = EXCLUDED.source_district,
				price_rub_m2 = EXCLUDED.price_rub_m2,
				yield_vs_bank_deposit = EXCLUDED.yield_vs_bank_deposit,
				source = EXCLUDED.source,
				source_file = EXCLUDED.source_file,
				updated_at = now()
		`,
			metric.PeriodMonth,
			metric.MunicipalityName,
			metric.SourceDistrict,
			metric.PriceRubM2,
			metric.YieldVsBankDeposit,
			staticEconomicSource,
			metric.SourceFile,
		)
		if err != nil {
			return 0, fmt.Errorf("save economic metric %s failed: %w", metric.MunicipalityName, err)
		}
		total += tag.RowsAffected()
	}
	return total, nil
}

func staticDataPath(dataDir string, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dataDir, name)
}

func stringProperty(properties map[string]any, key string) string {
	value, ok := properties[key]
	if !ok || value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func requiredStringProperty(properties map[string]any, key string) (string, error) {
	value := stringProperty(properties, key)
	if value == "" {
		return "", fmt.Errorf("property %s is empty", key)
	}
	return value, nil
}

func csvColumns(header []string) map[string]int {
	columns := make(map[string]int, len(header))
	for i, column := range header {
		columns[strings.TrimSpace(column)] = i
	}
	return columns
}

func csvValue(record []string, columns map[string]int, column string) string {
	index, ok := columns[column]
	if !ok || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}
