package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"

	"web-city/api/internal/store"
)

const (
	MethodNearestDistance       = "nearest_distance"
	MethodCountInRadius         = "count_in_radius"
	MethodDistinctTypesInRadius = "distinct_types_in_radius"
	MethodAreaIntersectionM2    = "area_intersection_m2"
	MethodDistrictMetric        = "district_metric"

	DirectionLowerIsBetter  = "lower_is_better"
	DirectionHigherIsBetter = "higher_is_better"

	defaultRadiusM = 1000.0
	maxRadiusM     = 5000.0
)

type Service struct {
	store       *store.Store
	config      Config
	weights     WeightsConfig
	profileByID map[string]ProfileWeights
}

type RequestError struct {
	Message string
}

func (e RequestError) Error() string {
	return e.Message
}

type Config struct {
	Indicators []IndicatorConfig `json:"indicators"`
}

type WeightsConfig struct {
	DefaultProfileID        string                        `json:"default_profile_id"`
	DefaultIndicatorWeights map[string]map[string]float64 `json:"default_indicator_weights"`
	Profiles                []ProfileWeights              `json:"profiles"`
}

type ProfileWeights struct {
	ID               string                        `json:"id"`
	Title            string                        `json:"title"`
	GroupWeights     map[string]float64            `json:"group_weights"`
	SectionWeights   map[string]map[string]float64 `json:"section_weights"`
	IndicatorWeights map[string]map[string]float64 `json:"indicator_weights,omitempty"`
}

type IndicatorConfig struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	GroupID      string   `json:"group_id"`
	GroupTitle   string   `json:"group_title"`
	SectionID    string   `json:"section_id"`
	SectionTitle string   `json:"section_title"`
	Method       string   `json:"method"`
	Category     string   `json:"category"`
	Subcategory  string   `json:"subcategory"`
	ObjectTypes  []string `json:"object_types"`
	MetricKey    string   `json:"metric_key"`
	Unit         string   `json:"unit"`
	Best         float64  `json:"best"`
	Worst        float64  `json:"worst"`
	Direction    string   `json:"direction"`
	RadiusM      float64  `json:"radius_m"`
	Weight       float64  `json:"weight"`
}

type EvaluateRequest struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Profile string  `json:"profile"`
	RadiusM float64 `json:"radius_m"`
}

type EvaluationResult struct {
	Score        *float64                   `json:"score"`
	Profile      string                     `json:"profile"`
	ProfileTitle string                     `json:"profile_title"`
	Lat          float64                    `json:"lat"`
	Lon          float64                    `json:"lon"`
	RadiusM      float64                    `json:"radius_m"`
	Municipality *store.MunicipalityContext `json:"municipality,omitempty"`
	Groups       []GroupResult              `json:"groups"`
	Indicators   []IndicatorResult          `json:"indicators"`
}

type GroupResult struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Weight   float64         `json:"weight"`
	Score    *float64        `json:"score"`
	Sections []SectionResult `json:"sections"`
}

type SectionResult struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Weight     float64           `json:"weight"`
	Score      *float64          `json:"score"`
	Indicators []IndicatorResult `json:"indicators"`
}

type IndicatorResult struct {
	ID            string                             `json:"id"`
	Title         string                             `json:"title"`
	GroupID       string                             `json:"group_id"`
	GroupTitle    string                             `json:"group_title"`
	SectionID     string                             `json:"section_id"`
	SectionTitle  string                             `json:"section_title"`
	Method        string                             `json:"method"`
	Unit          string                             `json:"unit"`
	Weight        float64                            `json:"weight"`
	RadiusM       *float64                           `json:"radius_m,omitempty"`
	RawValue      *float64                           `json:"raw_value"`
	Score         *float64                           `json:"score"`
	Status        string                             `json:"status"`
	Message       string                             `json:"message,omitempty"`
	NearestObject *store.NearestInfrastructureObject `json:"nearest_object,omitempty"`
}

func NewService(store *store.Store, configPath string, weightsPath string) (*Service, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	weights, err := LoadWeightsConfig(weightsPath)
	if err != nil {
		return nil, err
	}
	if err := validateWeightsAgainstIndicators(weights, config); err != nil {
		return nil, err
	}

	return &Service{
		store:       store,
		config:      config,
		weights:     weights,
		profileByID: profileMap(weights),
	}, nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read assessment config failed: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("decode assessment config failed: %w", err)
	}

	if err := validateConfig(config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func LoadWeightsConfig(path string) (WeightsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WeightsConfig{}, fmt.Errorf("read assessment weights failed: %w", err)
	}

	var config WeightsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return WeightsConfig{}, fmt.Errorf("decode assessment weights failed: %w", err)
	}

	if err := validateWeightsConfig(config); err != nil {
		return WeightsConfig{}, err
	}

	return config, nil
}

func (s *Service) Evaluate(ctx context.Context, req EvaluateRequest) (EvaluationResult, error) {
	req.Profile = strings.TrimSpace(req.Profile)
	if req.Profile == "" {
		req.Profile = s.weights.DefaultProfileID
	}

	profile, ok := s.profileByID[req.Profile]
	if !ok {
		return EvaluationResult{}, RequestError{Message: "unknown assessment profile"}
	}

	if err := validatePoint(req.Lon, req.Lat); err != nil {
		return EvaluationResult{}, err
	}

	radiusM := req.RadiusM
	if radiusM <= 0 {
		radiusM = defaultRadiusM
	}
	if radiusM > maxRadiusM {
		radiusM = maxRadiusM
	}

	result := EvaluationResult{
		Profile:      profile.ID,
		ProfileTitle: profile.Title,
		Lat:          req.Lat,
		Lon:          req.Lon,
		RadiusM:      radiusM,
	}

	municipality, err := s.store.FindMunicipalityByPoint(ctx, req.Lon, req.Lat)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return EvaluationResult{}, fmt.Errorf("find municipality failed: %w", err)
	}
	result.Municipality = municipality

	indicators := make([]IndicatorResult, 0, len(s.config.Indicators))
	for _, indicator := range s.config.Indicators {
		item, err := s.evaluateIndicator(ctx, indicator, profile, req.Lon, req.Lat, radiusM, municipality)
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("evaluate indicator %s failed: %w", indicator.ID, err)
		}
		indicators = append(indicators, item)
	}

	result.Indicators = indicators
	result.Groups = groupIndicators(indicators, profile)
	result.Score = aggregateGroupScores(result.Groups)

	return result, nil
}

func (s *Service) evaluateIndicator(
	ctx context.Context,
	cfg IndicatorConfig,
	profile ProfileWeights,
	lon float64,
	lat float64,
	requestRadiusM float64,
	municipality *store.MunicipalityContext,
) (IndicatorResult, error) {
	result := IndicatorResult{
		ID:           cfg.ID,
		Title:        cfg.Title,
		GroupID:      cfg.GroupID,
		GroupTitle:   cfg.GroupTitle,
		SectionID:    cfg.SectionID,
		SectionTitle: cfg.SectionTitle,
		Method:       cfg.Method,
		Unit:         cfg.Unit,
		Weight:       s.indicatorWeight(profile, cfg),
		Status:       "ok",
	}

	radiusM := cfg.RadiusM
	if radiusM <= 0 {
		radiusM = requestRadiusM
	}

	switch cfg.Method {
	case MethodNearestDistance:
		nearest, err := s.store.NearestInfrastructureObject(ctx, lon, lat, selectorFromConfig(cfg))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				result.Status = "no_data"
				result.Message = "matching infrastructure objects were not found"
				return result, nil
			}
			return IndicatorResult{}, err
		}

		result.NearestObject = nearest
		result.RawValue = floatPtr(round(nearest.DistanceM, 3))

	case MethodCountInRadius:
		count, err := s.store.CountInfrastructureObjectsInRadius(ctx, lon, lat, radiusM, selectorFromConfig(cfg))
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(float64(count))

	case MethodDistinctTypesInRadius:
		count, err := s.store.CountDistinctInfrastructureObjectTypesInRadius(ctx, lon, lat, radiusM, selectorFromConfig(cfg))
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(float64(count))

	case MethodAreaIntersectionM2:
		areaM2, err := s.store.InfrastructureAreaIntersectionM2(ctx, lon, lat, radiusM, selectorFromConfig(cfg))
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(round(areaM2, 3))

	case MethodDistrictMetric:
		value, ok := districtMetricValue(cfg.MetricKey, municipality)
		if !ok {
			result.Status = "no_data"
			result.Message = "district metric is not available for this municipality"
			return result, nil
		}
		result.RawValue = floatPtr(value)

	default:
		return IndicatorResult{}, fmt.Errorf("unsupported method %s", cfg.Method)
	}

	if result.RawValue != nil {
		score := normalize(*result.RawValue, cfg)
		result.Score = floatPtr(round(score, 4))
	}

	return result, nil
}

func selectorFromConfig(cfg IndicatorConfig) store.InfrastructureSelector {
	return store.InfrastructureSelector{
		Category:    cfg.Category,
		Subcategory: cfg.Subcategory,
		ObjectTypes: cfg.ObjectTypes,
	}
}

func districtMetricValue(metricKey string, municipality *store.MunicipalityContext) (float64, bool) {
	if municipality == nil {
		return 0, false
	}

	switch metricKey {
	case "price_rub_m2":
		if municipality.PriceRubM2 == nil {
			return 0, false
		}
		return float64(*municipality.PriceRubM2), true
	case "yield_vs_bank_deposit":
		if municipality.YieldVsBankDeposit == nil {
			return 0, false
		}
		return *municipality.YieldVsBankDeposit, true
	default:
		return 0, false
	}
}

func normalize(value float64, cfg IndicatorConfig) float64 {
	if cfg.Direction == DirectionLowerIsBetter {
		if value <= cfg.Best {
			return 1
		}
		if value >= cfg.Worst {
			return 0
		}
		return (cfg.Worst - value) / (cfg.Worst - cfg.Best)
	}

	if value >= cfg.Best {
		return 1
	}
	if value <= cfg.Worst {
		return 0
	}
	return (value - cfg.Worst) / (cfg.Best - cfg.Worst)
}

func groupIndicators(indicators []IndicatorResult, profile ProfileWeights) []GroupResult {
	groups := make([]GroupResult, 0)
	groupIndex := make(map[string]int)
	sectionIndex := make(map[string]map[string]int)

	for _, indicator := range indicators {
		groupPos, ok := groupIndex[indicator.GroupID]
		if !ok {
			groupPos = len(groups)
			groupIndex[indicator.GroupID] = groupPos
			sectionIndex[indicator.GroupID] = make(map[string]int)
			groups = append(groups, GroupResult{
				ID:     indicator.GroupID,
				Title:  indicator.GroupTitle,
				Weight: groupWeight(profile, indicator.GroupID),
			})
		}

		sectionPos, ok := sectionIndex[indicator.GroupID][indicator.SectionID]
		if !ok {
			sectionPos = len(groups[groupPos].Sections)
			sectionIndex[indicator.GroupID][indicator.SectionID] = sectionPos
			groups[groupPos].Sections = append(groups[groupPos].Sections, SectionResult{
				ID:     indicator.SectionID,
				Title:  indicator.SectionTitle,
				Weight: sectionWeight(profile, indicator.GroupID, indicator.SectionID),
			})
		}

		groups[groupPos].Sections[sectionPos].Indicators = append(groups[groupPos].Sections[sectionPos].Indicators, indicator)
	}

	for groupPos := range groups {
		for sectionPos := range groups[groupPos].Sections {
			groups[groupPos].Sections[sectionPos].Score = aggregateIndicatorScores(groups[groupPos].Sections[sectionPos].Indicators)
		}
		groups[groupPos].Score = aggregateSectionScores(groups[groupPos].Sections)
	}

	return groups
}

func aggregateIndicatorScores(indicators []IndicatorResult) *float64 {
	sum := 0.0
	weightSum := 0.0
	for _, indicator := range indicators {
		if indicator.Score == nil || indicator.Weight <= 0 {
			continue
		}
		sum += *indicator.Score * indicator.Weight
		weightSum += indicator.Weight
	}
	if weightSum == 0 {
		return nil
	}
	return floatPtr(round(sum/weightSum, 4))
}

func aggregateSectionScores(sections []SectionResult) *float64 {
	sum := 0.0
	weightSum := 0.0
	for _, section := range sections {
		if section.Score == nil || section.Weight <= 0 {
			continue
		}
		sum += *section.Score * section.Weight
		weightSum += section.Weight
	}
	if weightSum == 0 {
		return nil
	}
	return floatPtr(round(sum/weightSum, 4))
}

func aggregateGroupScores(groups []GroupResult) *float64 {
	sum := 0.0
	weightSum := 0.0
	for _, group := range groups {
		if group.Score == nil || group.Weight <= 0 {
			continue
		}
		sum += *group.Score * group.Weight
		weightSum += group.Weight
	}
	if weightSum == 0 {
		return nil
	}
	return floatPtr(round((sum/weightSum)*100, 2))
}

func validateConfig(config Config) error {
	if len(config.Indicators) == 0 {
		return fmt.Errorf("assessment config has no indicators")
	}

	ids := make(map[string]struct{}, len(config.Indicators))
	for i, indicator := range config.Indicators {
		if strings.TrimSpace(indicator.ID) == "" {
			return fmt.Errorf("indicator %d has empty id", i)
		}
		if _, ok := ids[indicator.ID]; ok {
			return fmt.Errorf("duplicate indicator id %s", indicator.ID)
		}
		ids[indicator.ID] = struct{}{}

		if !isSupportedMethod(indicator.Method) {
			return fmt.Errorf("indicator %s has unsupported method %s", indicator.ID, indicator.Method)
		}
		if indicator.Direction != DirectionLowerIsBetter && indicator.Direction != DirectionHigherIsBetter {
			return fmt.Errorf("indicator %s has unsupported direction %s", indicator.ID, indicator.Direction)
		}
		if indicator.Best == indicator.Worst {
			return fmt.Errorf("indicator %s has equal best and worst values", indicator.ID)
		}
	}

	return nil
}

func validateWeightsConfig(config WeightsConfig) error {
	if strings.TrimSpace(config.DefaultProfileID) == "" {
		return fmt.Errorf("assessment weights default_profile_id is empty")
	}
	if len(config.Profiles) == 0 {
		return fmt.Errorf("assessment weights has no profiles")
	}
	if len(config.DefaultIndicatorWeights) == 0 {
		return fmt.Errorf("assessment weights has no default_indicator_weights")
	}
	if err := validateNestedWeights("default_indicator_weights", config.DefaultIndicatorWeights); err != nil {
		return err
	}

	ids := make(map[string]struct{}, len(config.Profiles))
	hasDefaultProfile := false
	for _, profile := range config.Profiles {
		profile.ID = strings.TrimSpace(profile.ID)
		if profile.ID == "" {
			return fmt.Errorf("assessment weights profile has empty id")
		}
		if _, ok := ids[profile.ID]; ok {
			return fmt.Errorf("duplicate assessment weights profile id %s", profile.ID)
		}
		ids[profile.ID] = struct{}{}
		if profile.ID == config.DefaultProfileID {
			hasDefaultProfile = true
		}

		if err := validateWeightMap(fmt.Sprintf("profile %s group_weights", profile.ID), profile.GroupWeights); err != nil {
			return err
		}
		if err := validateNestedWeights(fmt.Sprintf("profile %s section_weights", profile.ID), profile.SectionWeights); err != nil {
			return err
		}
		if len(profile.IndicatorWeights) > 0 {
			if err := validateNestedWeights(fmt.Sprintf("profile %s indicator_weights", profile.ID), profile.IndicatorWeights); err != nil {
				return err
			}
		}
	}

	if !hasDefaultProfile {
		return fmt.Errorf("assessment weights default profile %s is not defined", config.DefaultProfileID)
	}

	return nil
}

func validateWeightsAgainstIndicators(weights WeightsConfig, config Config) error {
	groups := make(map[string]struct{})
	sectionsByGroup := make(map[string]map[string]struct{})
	indicatorsBySection := make(map[string]map[string]struct{})

	for _, indicator := range config.Indicators {
		groups[indicator.GroupID] = struct{}{}
		if sectionsByGroup[indicator.GroupID] == nil {
			sectionsByGroup[indicator.GroupID] = make(map[string]struct{})
		}
		sectionsByGroup[indicator.GroupID][indicator.SectionID] = struct{}{}
		if indicatorsBySection[indicator.SectionID] == nil {
			indicatorsBySection[indicator.SectionID] = make(map[string]struct{})
		}
		indicatorsBySection[indicator.SectionID][indicator.ID] = struct{}{}
	}

	if err := requireDefaultIndicatorWeights(weights.DefaultIndicatorWeights, indicatorsBySection); err != nil {
		return err
	}

	for _, profile := range weights.Profiles {
		if err := requireExactKeys(fmt.Sprintf("profile %s group_weights", profile.ID), profile.GroupWeights, groups); err != nil {
			return err
		}
		for groupID, sectionIDs := range sectionsByGroup {
			if profile.SectionWeights[groupID] == nil {
				return fmt.Errorf("profile %s section_weights is missing group %s", profile.ID, groupID)
			}
			if err := requireExactKeys(fmt.Sprintf("profile %s section_weights.%s", profile.ID, groupID), profile.SectionWeights[groupID], sectionIDs); err != nil {
				return err
			}
		}
		for sectionID, override := range profile.IndicatorWeights {
			expected, ok := indicatorsBySection[sectionID]
			if !ok {
				return fmt.Errorf("profile %s indicator_weights has unknown section %s", profile.ID, sectionID)
			}
			if err := requireExactKeys(fmt.Sprintf("profile %s indicator_weights.%s", profile.ID, sectionID), override, expected); err != nil {
				return err
			}
		}
	}

	return nil
}

func requireDefaultIndicatorWeights(weights map[string]map[string]float64, indicatorsBySection map[string]map[string]struct{}) error {
	for sectionID, indicatorIDs := range indicatorsBySection {
		actual, ok := weights[sectionID]
		if !ok {
			return fmt.Errorf("default_indicator_weights is missing section %s", sectionID)
		}
		if err := requireExactKeys("default_indicator_weights."+sectionID, actual, indicatorIDs); err != nil {
			return err
		}
	}
	for sectionID := range weights {
		if _, ok := indicatorsBySection[sectionID]; !ok {
			return fmt.Errorf("default_indicator_weights has unknown section %s", sectionID)
		}
	}
	return nil
}

func validateNestedWeights(name string, weights map[string]map[string]float64) error {
	if len(weights) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	for key, values := range weights {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s has empty key", name)
		}
		if err := validateWeightMap(name+"."+key, values); err != nil {
			return err
		}
	}
	return nil
}

func validateWeightMap(name string, weights map[string]float64) error {
	if len(weights) == 0 {
		return fmt.Errorf("%s is empty", name)
	}

	sum := 0.0
	for key, weight := range weights {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s has empty key", name)
		}
		if weight < 0 {
			return fmt.Errorf("%s.%s has negative weight", name, key)
		}
		sum += weight
	}
	if math.Abs(sum-1) > 0.001 {
		return fmt.Errorf("%s weights must sum to 1, got %.6f", name, sum)
	}

	return nil
}

func requireExactKeys(name string, actual map[string]float64, expected map[string]struct{}) error {
	for key := range expected {
		if _, ok := actual[key]; !ok {
			return fmt.Errorf("%s is missing key %s", name, key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("%s has unknown key %s", name, key)
		}
	}
	return nil
}

func profileMap(config WeightsConfig) map[string]ProfileWeights {
	result := make(map[string]ProfileWeights, len(config.Profiles))
	for _, profile := range config.Profiles {
		result[profile.ID] = profile
	}
	return result
}

func groupWeight(profile ProfileWeights, groupID string) float64 {
	return profile.GroupWeights[groupID]
}

func sectionWeight(profile ProfileWeights, groupID string, sectionID string) float64 {
	groupWeights := profile.SectionWeights[groupID]
	if groupWeights == nil {
		return 0
	}
	return groupWeights[sectionID]
}

func (s *Service) indicatorWeight(profile ProfileWeights, cfg IndicatorConfig) float64 {
	if profile.IndicatorWeights != nil {
		if weights := profile.IndicatorWeights[cfg.SectionID]; weights != nil {
			return weights[cfg.ID]
		}
	}
	if weights := s.weights.DefaultIndicatorWeights[cfg.SectionID]; weights != nil {
		return weights[cfg.ID]
	}
	return 0
}

func isSupportedMethod(method string) bool {
	switch method {
	case MethodNearestDistance, MethodCountInRadius, MethodDistinctTypesInRadius, MethodAreaIntersectionM2, MethodDistrictMetric:
		return true
	default:
		return false
	}
}

func validatePoint(lon, lat float64) error {
	if math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) {
		return RequestError{Message: "coordinates must be finite numbers"}
	}
	if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
		return RequestError{Message: "coordinates are outside valid lon/lat ranges"}
	}
	return nil
}

func floatPtr(value float64) *float64 {
	return &value
}

func round(value float64, precision int) float64 {
	multiplier := math.Pow10(precision)
	return math.Round(value*multiplier) / multiplier
}
