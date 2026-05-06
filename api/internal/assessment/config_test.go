package assessment

import (
	"errors"
	"math"
	"testing"
)

const (
	testConfigPath  = "../../config/assessment_indicators.json"
	testWeightsPath = "../../config/assessment_weights.json"
)

func TestAssessmentConfigFilesAreValid(t *testing.T) {
	config, err := LoadConfig(testConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(config.Indicators) == 0 {
		t.Fatal("expected assessment indicators")
	}

	weights, err := LoadWeightsConfig(testWeightsPath)
	if err != nil {
		t.Fatalf("LoadWeightsConfig() error = %v", err)
	}
	if err := validateWeightsAgainstIndicators(weights, config); err != nil {
		t.Fatalf("validateWeightsAgainstIndicators() error = %v", err)
	}
}

func TestAssessmentConfigMethodFieldsAreValid(t *testing.T) {
	config, err := LoadConfig(testConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	for _, indicator := range config.Indicators {
		if indicator.Direction == DirectionLowerIsBetter && indicator.Best >= indicator.Worst {
			t.Fatalf("indicator %s: lower_is_better expects best < worst, got best=%v worst=%v", indicator.ID, indicator.Best, indicator.Worst)
		}
		if indicator.Direction == DirectionHigherIsBetter && indicator.Best <= indicator.Worst {
			t.Fatalf("indicator %s: higher_is_better expects best > worst, got best=%v worst=%v", indicator.ID, indicator.Best, indicator.Worst)
		}

		switch indicator.Method {
		case MethodNearestDistance, MethodCountInRadius, MethodDistinctTypesInRadius, MethodAreaIntersectionM2:
			if indicator.Category == "" {
				t.Fatalf("indicator %s: %s requires category", indicator.ID, indicator.Method)
			}
			if indicator.Subcategory == "" {
				t.Fatalf("indicator %s: %s requires subcategory", indicator.ID, indicator.Method)
			}
			if len(indicator.ObjectTypes) == 0 {
				t.Fatalf("indicator %s: %s requires object_types", indicator.ID, indicator.Method)
			}
		case MethodDistrictMetric:
			if indicator.MetricKey == "" {
				t.Fatalf("indicator %s: district_metric requires metric_key", indicator.ID)
			}
		default:
			t.Fatalf("indicator %s: unsupported method %s", indicator.ID, indicator.Method)
		}
	}
}

func TestAssessmentConfigResponseWeightsAreComplete(t *testing.T) {
	service, err := NewService(nil, testConfigPath, testWeightsPath)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	response := service.ConfigResponse()
	if response.DefaultProfileID == "" {
		t.Fatal("expected default profile id")
	}
	if len(response.Profiles) == 0 {
		t.Fatal("expected profiles")
	}
	if len(response.Groups) == 0 {
		t.Fatal("expected assessment groups")
	}

	groupIDs := make(map[string]struct{}, len(response.Groups))
	sectionsByGroup := make(map[string]map[string]struct{})
	subsectionsBySection := make(map[string]map[string]struct{})
	indicatorsBySubsection := make(map[string]map[string]struct{})

	for _, group := range response.Groups {
		groupIDs[group.ID] = struct{}{}
		sectionsByGroup[group.ID] = make(map[string]struct{}, len(group.Sections))
		for _, section := range group.Sections {
			sectionsByGroup[group.ID][section.ID] = struct{}{}
			subsectionsBySection[section.ID] = make(map[string]struct{}, len(section.Subsections))
			for _, subsection := range section.Subsections {
				subsectionsBySection[section.ID][subsection.ID] = struct{}{}
				indicatorsBySubsection[subsection.ID] = make(map[string]struct{}, len(subsection.Indicators))
				for _, indicator := range subsection.Indicators {
					indicatorsBySubsection[subsection.ID][indicator.ID] = struct{}{}
				}
			}
		}
	}

	hasDefault := false
	for _, profile := range response.Profiles {
		if profile.ID == response.DefaultProfileID {
			hasDefault = true
		}
		assertPercentMap(t, profile.ID+" group weights", profile.GroupWeightsPercent, groupIDs)
		assertNestedPercentMap(t, profile.ID+" section weights", profile.SectionWeightsPercent, sectionsByGroup)
		assertNestedPercentMap(t, profile.ID+" subsection weights", profile.SubsectionWeightsPercent, subsectionsBySection)
		assertNestedPercentMap(t, profile.ID+" indicator weights", profile.IndicatorWeightsPercent, indicatorsBySubsection)
	}
	if !hasDefault {
		t.Fatalf("default profile %s is absent from profiles", response.DefaultProfileID)
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		cfg  IndicatorConfig
		in   float64
		want float64
	}{
		{
			name: "lower is better at best",
			cfg:  IndicatorConfig{Best: 300, Worst: 1200, Direction: DirectionLowerIsBetter},
			in:   250,
			want: 1,
		},
		{
			name: "lower is better at worst",
			cfg:  IndicatorConfig{Best: 300, Worst: 1200, Direction: DirectionLowerIsBetter},
			in:   1300,
			want: 0,
		},
		{
			name: "lower is better middle",
			cfg:  IndicatorConfig{Best: 300, Worst: 1200, Direction: DirectionLowerIsBetter},
			in:   750,
			want: 0.5,
		},
		{
			name: "higher is better at best",
			cfg:  IndicatorConfig{Best: 10, Worst: 0, Direction: DirectionHigherIsBetter},
			in:   12,
			want: 1,
		},
		{
			name: "higher is better at worst",
			cfg:  IndicatorConfig{Best: 10, Worst: 0, Direction: DirectionHigherIsBetter},
			in:   -1,
			want: 0,
		},
		{
			name: "higher is better middle",
			cfg:  IndicatorConfig{Best: 10, Worst: 0, Direction: DirectionHigherIsBetter},
			in:   4,
			want: 0.4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalize(tt.in, tt.cfg)
			if math.Abs(got-tt.want) > 0.000001 {
				t.Fatalf("normalize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidatePoint(t *testing.T) {
	tests := []struct {
		name    string
		lon     float64
		lat     float64
		wantErr bool
	}{
		{name: "valid", lon: 37.618423, lat: 55.751244},
		{name: "nan lon", lon: math.NaN(), lat: 55.751244, wantErr: true},
		{name: "infinite lat", lon: 37.618423, lat: math.Inf(1), wantErr: true},
		{name: "lon out of range", lon: 181, lat: 55.751244, wantErr: true},
		{name: "lat out of range", lon: 37.618423, lat: 91, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePoint(tt.lon, tt.lat)
			if tt.wantErr {
				var requestErr RequestError
				if !errors.As(err, &requestErr) {
					t.Fatalf("validatePoint() error = %v, want RequestError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("validatePoint() error = %v, want nil", err)
			}
		})
	}
}

func assertNestedPercentMap(t *testing.T, name string, actual map[string]map[string]float64, expected map[string]map[string]struct{}) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("%s: got %d parent groups, want %d", name, len(actual), len(expected))
	}
	for parentID, expectedChildren := range expected {
		children, ok := actual[parentID]
		if !ok {
			t.Fatalf("%s: missing parent %s", name, parentID)
		}
		assertPercentMap(t, name+"."+parentID, children, expectedChildren)
	}
	for parentID := range actual {
		if _, ok := expected[parentID]; !ok {
			t.Fatalf("%s: unexpected parent %s", name, parentID)
		}
	}
}

func assertPercentMap(t *testing.T, name string, actual map[string]float64, expected map[string]struct{}) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Fatalf("%s: got %d keys, want %d", name, len(actual), len(expected))
	}

	sum := 0.0
	for id := range expected {
		value, ok := actual[id]
		if !ok {
			t.Fatalf("%s: missing key %s", name, id)
		}
		if value < 0 {
			t.Fatalf("%s.%s: negative weight %v", name, id, value)
		}
		sum += value
	}
	for id := range actual {
		if _, ok := expected[id]; !ok {
			t.Fatalf("%s: unexpected key %s", name, id)
		}
	}
	if math.Abs(sum-100) > 0.001 {
		t.Fatalf("%s: weights must sum to 100, got %.6f", name, sum)
	}
}
