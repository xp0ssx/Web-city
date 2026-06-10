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

	maxUsedObjects = 100
	maxUsedAreas   = 50

	generalProfileID    = "general"
	generalProfileTitle = "Общий профиль"
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
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	GroupID         string   `json:"group_id"`
	GroupTitle      string   `json:"group_title"`
	SectionID       string   `json:"section_id"`
	SectionTitle    string   `json:"section_title"`
	SubsectionID    string   `json:"subsection_id"`
	SubsectionTitle string   `json:"subsection_title"`
	Method          string   `json:"method"`
	Category        string   `json:"category"`
	Subcategory     string   `json:"subcategory"`
	ObjectTypes     []string `json:"object_types"`
	MetricKey       string   `json:"metric_key"`
	Unit            string   `json:"unit"`
	Best            float64  `json:"best"`
	Worst           float64  `json:"worst"`
	Direction       string   `json:"direction"`
	RadiusM         float64  `json:"radius_m"`
	Weight          float64  `json:"weight"`
}

type EvaluateRequest struct {
	Lat                      float64                       `json:"lat"`
	Lon                      float64                       `json:"lon"`
	Profile                  string                        `json:"profile"`
	RadiusM                  float64                       `json:"radius_m"`
	ProfileWeightsPercent    map[string]float64            `json:"profile_weights_percent"`
	GroupWeightsPercent      map[string]float64            `json:"group_weights_percent"`
	SectionWeightsPercent    map[string]map[string]float64 `json:"section_weights_percent"`
	SubsectionWeightsPercent map[string]map[string]float64 `json:"subsection_weights_percent"`
	IndicatorWeightsPercent  map[string]map[string]float64 `json:"indicator_weights_percent"`
}

type AssessmentConfigResponse struct {
	DefaultProfileID string                  `json:"default_profile_id"`
	Profiles         []AssessmentProfileInfo `json:"profiles"`
	Groups           []AssessmentGroupConfig `json:"groups"`
}

type AssessmentProfileInfo struct {
	ID                       string                        `json:"id"`
	Title                    string                        `json:"title"`
	ProfileWeightsPercent    map[string]float64            `json:"profile_weights_percent,omitempty"`
	GroupWeightsPercent      map[string]float64            `json:"group_weights_percent"`
	SectionWeightsPercent    map[string]map[string]float64 `json:"section_weights_percent"`
	SubsectionWeightsPercent map[string]map[string]float64 `json:"subsection_weights_percent"`
	IndicatorWeightsPercent  map[string]map[string]float64 `json:"indicator_weights_percent"`
}

type AssessmentGroupConfig struct {
	ID       string                    `json:"id"`
	Title    string                    `json:"title"`
	Sections []AssessmentSectionConfig `json:"sections"`
}

type AssessmentSectionConfig struct {
	ID          string                       `json:"id"`
	Title       string                       `json:"title"`
	Subsections []AssessmentSubsectionConfig `json:"subsections"`
}

type AssessmentSubsectionConfig struct {
	ID         string                      `json:"id"`
	Title      string                      `json:"title"`
	Indicators []AssessmentIndicatorConfig `json:"indicators"`
}

type AssessmentIndicatorConfig struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type evaluationWeights struct {
	ID                string
	Title             string
	GroupWeights      map[string]float64
	SectionWeights    map[string]map[string]float64
	SubsectionWeights map[string]map[string]float64
	IndicatorWeights  map[string]map[string]float64
}

type EvaluationResult struct {
	Score            *float64                   `json:"score"`
	Profile          string                     `json:"profile"`
	ProfileTitle     string                     `json:"profile_title"`
	WeightsMode      string                     `json:"weights_mode"`
	BaseProfile      string                     `json:"base_profile,omitempty"`
	BaseProfileTitle string                     `json:"base_profile_title,omitempty"`
	ProfileScores    []ProfileScoreResult       `json:"profile_scores,omitempty"`
	Lat              float64                    `json:"lat"`
	Lon              float64                    `json:"lon"`
	RadiusM          float64                    `json:"radius_m"`
	Municipality     *store.MunicipalityContext `json:"municipality,omitempty"`
	Groups           []GroupResult              `json:"groups"`
	Indicators       []IndicatorResult          `json:"indicators"`
}

type ProfileScoreResult struct {
	Profile      string   `json:"profile"`
	ProfileTitle string   `json:"profile_title"`
	Weight       float64  `json:"weight"`
	Score        *float64 `json:"score"`
}

type GroupResult struct {
	ID       string          `json:"id"`
	Title    string          `json:"title"`
	Weight   float64         `json:"weight"`
	Score    *float64        `json:"score"`
	Sections []SectionResult `json:"sections"`
}

type SectionResult struct {
	ID          string             `json:"id"`
	Title       string             `json:"title"`
	Weight      float64            `json:"weight"`
	Score       *float64           `json:"score"`
	Subsections []SubsectionResult `json:"subsections"`
}

type SubsectionResult struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Weight     float64           `json:"weight"`
	Score      *float64          `json:"score"`
	Indicators []IndicatorResult `json:"indicators"`
}

type IndicatorResult struct {
	ID              string                                 `json:"id"`
	Title           string                                 `json:"title"`
	GroupID         string                                 `json:"group_id"`
	GroupTitle      string                                 `json:"group_title"`
	SectionID       string                                 `json:"section_id"`
	SectionTitle    string                                 `json:"section_title"`
	SubsectionID    string                                 `json:"subsection_id"`
	SubsectionTitle string                                 `json:"subsection_title"`
	Method          string                                 `json:"method"`
	Unit            string                                 `json:"unit"`
	Weight          float64                                `json:"weight"`
	RadiusM         *float64                               `json:"radius_m,omitempty"`
	RawValue        *float64                               `json:"raw_value"`
	Score           *float64                               `json:"score"`
	Status          string                                 `json:"status"`
	Message         string                                 `json:"message,omitempty"`
	NearestObject   *store.NearestInfrastructureObject     `json:"nearest_object,omitempty"`
	UsedObjects     []store.NearestInfrastructureObject    `json:"used_objects,omitempty"`
	UsedAreas       []store.InfrastructureAreaIntersection `json:"used_areas,omitempty"`
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

	if req.Profile == generalProfileID {
		return s.evaluateGeneralProfile(ctx, req)
	}
	if len(req.ProfileWeightsPercent) > 0 {
		return EvaluationResult{}, RequestError{Message: "profile weights can be used only with general profile"}
	}

	customWeightsUsed := hasTreeCustomWeights(req)
	profile, ok := s.profileByID[req.Profile]
	if !ok {
		return EvaluationResult{}, RequestError{Message: "unknown assessment profile"}
	}
	evalWeights := s.evaluationWeights(profile)

	if len(req.GroupWeightsPercent) > 0 {
		groupWeights, err := customPercentWeights("custom group weights", req.GroupWeightsPercent, evalWeights.GroupWeights)
		if err != nil {
			return EvaluationResult{}, err
		}
		evalWeights.GroupWeights = groupWeights
	}
	if len(req.SectionWeightsPercent) > 0 {
		sectionWeights, err := customNestedPercentWeights("custom section weights", req.SectionWeightsPercent, evalWeights.SectionWeights)
		if err != nil {
			return EvaluationResult{}, err
		}
		evalWeights.SectionWeights = sectionWeights
	}
	if len(req.SubsectionWeightsPercent) > 0 {
		subsectionWeights, err := customNestedPercentWeights("custom subsection weights", req.SubsectionWeightsPercent, evalWeights.SubsectionWeights)
		if err != nil {
			return EvaluationResult{}, err
		}
		evalWeights.SubsectionWeights = subsectionWeights
	}
	if len(req.IndicatorWeightsPercent) > 0 {
		indicatorWeights, err := customNestedPercentWeights("custom indicator weights", req.IndicatorWeightsPercent, evalWeights.IndicatorWeights)
		if err != nil {
			return EvaluationResult{}, err
		}
		evalWeights.IndicatorWeights = indicatorWeights
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
		Profile:      evalWeights.ID,
		ProfileTitle: evalWeights.Title,
		WeightsMode:  "profile",
		Lat:          req.Lat,
		Lon:          req.Lon,
		RadiusM:      radiusM,
	}
	if customWeightsUsed {
		result.Profile = "custom"
		result.ProfileTitle = "Пользовательский профиль"
		result.WeightsMode = "custom"
		result.BaseProfile = evalWeights.ID
		result.BaseProfileTitle = evalWeights.Title
	}

	municipality, err := s.store.FindMunicipalityByPoint(ctx, req.Lon, req.Lat)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return EvaluationResult{}, RequestError{Message: "point is outside Moscow boundaries"}
		}
		return EvaluationResult{}, fmt.Errorf("find municipality failed: %w", err)
	}
	result.Municipality = municipality

	indicators, err := s.evaluateIndicators(ctx, evalWeights, req.Lon, req.Lat, radiusM, municipality)
	if err != nil {
		return EvaluationResult{}, err
	}

	result.Indicators = indicators
	result.Groups = groupIndicators(indicators, evalWeights)
	result.Score = aggregateGroupScores(result.Groups)

	return result, nil
}

func (s *Service) evaluateGeneralProfile(ctx context.Context, req EvaluateRequest) (EvaluationResult, error) {
	if hasTreeCustomWeights(req) {
		return EvaluationResult{}, RequestError{Message: "general profile supports only profile weights"}
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

	municipality, err := s.store.FindMunicipalityByPoint(ctx, req.Lon, req.Lat)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return EvaluationResult{}, RequestError{Message: "point is outside Moscow boundaries"}
		}
		return EvaluationResult{}, fmt.Errorf("find municipality failed: %w", err)
	}

	generalWeights := s.generalEvaluationWeights()
	measuredIndicators, err := s.evaluateIndicators(ctx, generalWeights, req.Lon, req.Lat, radiusM, municipality)
	if err != nil {
		return EvaluationResult{}, err
	}

	profileWeights := s.defaultGeneralProfileWeights()
	weightsMode := "profile"
	if len(req.ProfileWeightsPercent) > 0 {
		profileWeights, err = customPercentWeights("custom profile weights", req.ProfileWeightsPercent, profileWeights)
		if err != nil {
			return EvaluationResult{}, err
		}
		weightsMode = "custom"
	}

	profileScores := make([]ProfileScoreResult, 0, len(s.weights.Profiles))
	sum := 0.0
	weightSum := 0.0

	for _, profile := range s.weights.Profiles {
		weights := s.evaluationWeights(profile)
		indicators := indicatorsWithWeights(measuredIndicators, weights)
		groups := groupIndicators(indicators, weights)
		score := aggregateGroupScores(groups)
		profileWeight := profileWeights[profile.ID]

		profileScores = append(profileScores, ProfileScoreResult{
			Profile:      weights.ID,
			ProfileTitle: weights.Title,
			Weight:       round(profileWeight, 4),
			Score:        score,
		})

		if score != nil {
			sum += *score * profileWeight
			weightSum += profileWeight
		}
	}

	resultIndicators := indicatorsWithWeights(measuredIndicators, generalWeights)
	result := EvaluationResult{
		Profile:       generalProfileID,
		ProfileTitle:  generalProfileTitle,
		WeightsMode:   weightsMode,
		ProfileScores: profileScores,
		Lat:           req.Lat,
		Lon:           req.Lon,
		RadiusM:       radiusM,
		Municipality:  municipality,
		Indicators:    resultIndicators,
		Groups:        groupIndicators(resultIndicators, generalWeights),
	}
	if weightSum > 0 {
		result.Score = floatPtr(round(sum/weightSum, 2))
	}

	return result, nil
}

func (s *Service) evaluateIndicators(
	ctx context.Context,
	weights evaluationWeights,
	lon float64,
	lat float64,
	radiusM float64,
	municipality *store.MunicipalityContext,
) ([]IndicatorResult, error) {
	indicators := make([]IndicatorResult, 0, len(s.config.Indicators))
	for _, indicator := range s.config.Indicators {
		item, err := s.evaluateIndicator(ctx, indicator, weights, lon, lat, radiusM, municipality)
		if err != nil {
			return nil, fmt.Errorf("evaluate indicator %s failed: %w", indicator.ID, err)
		}
		indicators = append(indicators, item)
	}

	return indicators, nil
}

func (s *Service) ConfigResponse() AssessmentConfigResponse {
	profiles := make([]AssessmentProfileInfo, 0, len(s.weights.Profiles)+1)
	generalWeights := s.generalEvaluationWeights()
	profiles = append(profiles, AssessmentProfileInfo{
		ID:                       generalWeights.ID,
		Title:                    generalWeights.Title,
		ProfileWeightsPercent:    percentMap(s.defaultGeneralProfileWeights()),
		GroupWeightsPercent:      percentMap(generalWeights.GroupWeights),
		SectionWeightsPercent:    nestedPercentMap(generalWeights.SectionWeights),
		SubsectionWeightsPercent: nestedPercentMap(generalWeights.SubsectionWeights),
		IndicatorWeightsPercent:  nestedPercentMap(generalWeights.IndicatorWeights),
	})

	for _, profile := range s.weights.Profiles {
		weights := s.evaluationWeights(profile)
		profiles = append(profiles, AssessmentProfileInfo{
			ID:                       weights.ID,
			Title:                    weights.Title,
			GroupWeightsPercent:      percentMap(weights.GroupWeights),
			SectionWeightsPercent:    nestedPercentMap(weights.SectionWeights),
			SubsectionWeightsPercent: nestedPercentMap(weights.SubsectionWeights),
			IndicatorWeightsPercent:  nestedPercentMap(weights.IndicatorWeights),
		})
	}

	return AssessmentConfigResponse{
		DefaultProfileID: s.weights.DefaultProfileID,
		Profiles:         profiles,
		Groups:           s.assessmentTree(),
	}
}

func (s *Service) evaluateIndicator(
	ctx context.Context,
	cfg IndicatorConfig,
	weights evaluationWeights,
	lon float64,
	lat float64,
	requestRadiusM float64,
	municipality *store.MunicipalityContext,
) (IndicatorResult, error) {
	result := IndicatorResult{
		ID:              cfg.ID,
		Title:           cfg.Title,
		GroupID:         cfg.GroupID,
		GroupTitle:      cfg.GroupTitle,
		SectionID:       cfg.SectionID,
		SectionTitle:    cfg.SectionTitle,
		SubsectionID:    cfg.SubsectionID,
		SubsectionTitle: cfg.SubsectionTitle,
		Method:          cfg.Method,
		Unit:            cfg.Unit,
		Weight:          indicatorWeight(weights, cfg.SubsectionID, cfg.ID),
		Status:          "ok",
	}

	radiusM := cfg.RadiusM
	if radiusM <= 0 {
		radiusM = requestRadiusM
	}

	switch cfg.Method {
	case MethodNearestDistance:
		selector := selectorFromConfig(cfg)
		nearest, err := s.store.NearestInfrastructureObject(ctx, lon, lat, selector)
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
		selector := selectorFromConfig(cfg)
		count, err := s.store.CountInfrastructureObjectsInRadius(ctx, lon, lat, radiusM, selector)
		if err != nil {
			return IndicatorResult{}, err
		}
		objects, err := s.store.InfrastructureObjectsInRadius(ctx, lon, lat, radiusM, selector, maxUsedObjects)
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(float64(count))
		result.UsedObjects = objects

	case MethodDistinctTypesInRadius:
		selector := selectorFromConfig(cfg)
		count, err := s.store.CountDistinctInfrastructureObjectTypesInRadius(ctx, lon, lat, radiusM, selector)
		if err != nil {
			return IndicatorResult{}, err
		}
		objects, err := s.store.InfrastructureObjectsInRadius(ctx, lon, lat, radiusM, selector, maxUsedObjects)
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(float64(count))
		result.UsedObjects = objects

	case MethodAreaIntersectionM2:
		selector := selectorFromConfig(cfg)
		areaM2, err := s.store.InfrastructureAreaIntersectionM2(ctx, lon, lat, radiusM, selector)
		if err != nil {
			return IndicatorResult{}, err
		}
		areas, err := s.store.InfrastructureAreaIntersections(ctx, lon, lat, radiusM, selector, maxUsedAreas)
		if err != nil {
			return IndicatorResult{}, err
		}
		result.RadiusM = floatPtr(radiusM)
		result.RawValue = floatPtr(round(areaM2, 3))
		result.UsedAreas = areas

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

func groupIndicators(indicators []IndicatorResult, weights evaluationWeights) []GroupResult {
	groups := make([]GroupResult, 0)
	groupIndex := make(map[string]int)
	sectionIndex := make(map[string]map[string]int)
	subsectionIndex := make(map[string]map[string]map[string]int)

	for _, indicator := range indicators {
		groupPos, ok := groupIndex[indicator.GroupID]
		if !ok {
			groupPos = len(groups)
			groupIndex[indicator.GroupID] = groupPos
			sectionIndex[indicator.GroupID] = make(map[string]int)
			subsectionIndex[indicator.GroupID] = make(map[string]map[string]int)
			groups = append(groups, GroupResult{
				ID:     indicator.GroupID,
				Title:  indicator.GroupTitle,
				Weight: groupWeight(weights, indicator.GroupID),
			})
		}

		sectionPos, ok := sectionIndex[indicator.GroupID][indicator.SectionID]
		if !ok {
			sectionPos = len(groups[groupPos].Sections)
			sectionIndex[indicator.GroupID][indicator.SectionID] = sectionPos
			subsectionIndex[indicator.GroupID][indicator.SectionID] = make(map[string]int)
			groups[groupPos].Sections = append(groups[groupPos].Sections, SectionResult{
				ID:     indicator.SectionID,
				Title:  indicator.SectionTitle,
				Weight: sectionWeight(weights, indicator.GroupID, indicator.SectionID),
			})
		}

		subsectionPos, ok := subsectionIndex[indicator.GroupID][indicator.SectionID][indicator.SubsectionID]
		if !ok {
			subsectionPos = len(groups[groupPos].Sections[sectionPos].Subsections)
			subsectionIndex[indicator.GroupID][indicator.SectionID][indicator.SubsectionID] = subsectionPos
			groups[groupPos].Sections[sectionPos].Subsections = append(groups[groupPos].Sections[sectionPos].Subsections, SubsectionResult{
				ID:     indicator.SubsectionID,
				Title:  indicator.SubsectionTitle,
				Weight: subsectionWeight(weights, indicator.SectionID, indicator.SubsectionID),
			})
		}

		subsections := groups[groupPos].Sections[sectionPos].Subsections
		subsections[subsectionPos].Indicators = append(subsections[subsectionPos].Indicators, indicatorWithoutMapFeatures(indicator))
		groups[groupPos].Sections[sectionPos].Subsections = subsections
	}

	for groupPos := range groups {
		for sectionPos := range groups[groupPos].Sections {
			for subsectionPos := range groups[groupPos].Sections[sectionPos].Subsections {
				subsection := &groups[groupPos].Sections[sectionPos].Subsections[subsectionPos]
				subsection.Weight = round(subsection.Weight*aggregateIndicatorWeight(subsection.Indicators), 4)
				subsection.Score = aggregateIndicatorScores(subsection.Indicators)
			}
			groups[groupPos].Sections[sectionPos].Score = aggregateSubsectionScores(groups[groupPos].Sections[sectionPos].Subsections)
		}
		groups[groupPos].Score = aggregateSectionScores(groups[groupPos].Sections)
	}

	return groups
}

func indicatorWithoutMapFeatures(indicator IndicatorResult) IndicatorResult {
	indicator.NearestObject = nil
	indicator.UsedObjects = nil
	indicator.UsedAreas = nil
	return indicator
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

func aggregateIndicatorWeight(indicators []IndicatorResult) float64 {
	weightSum := 0.0
	for _, indicator := range indicators {
		if indicator.Score == nil || indicator.Weight <= 0 {
			continue
		}
		weightSum += indicator.Weight
	}
	return round(weightSum, 4)
}

func aggregateSubsectionScores(subsections []SubsectionResult) *float64 {
	sum := 0.0
	weightSum := 0.0
	for _, subsection := range subsections {
		if subsection.Score == nil || subsection.Weight <= 0 {
			continue
		}
		sum += *subsection.Score * subsection.Weight
		weightSum += subsection.Weight
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

		if strings.TrimSpace(indicator.GroupID) == "" {
			return fmt.Errorf("indicator %s has empty group_id", indicator.ID)
		}
		if strings.TrimSpace(indicator.SectionID) == "" {
			return fmt.Errorf("indicator %s has empty section_id", indicator.ID)
		}
		if strings.TrimSpace(indicator.SubsectionID) == "" {
			return fmt.Errorf("indicator %s has empty subsection_id", indicator.ID)
		}
		if strings.TrimSpace(indicator.SubsectionTitle) == "" {
			return fmt.Errorf("indicator %s has empty subsection_title", indicator.ID)
		}
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
	if config.DefaultProfileID == generalProfileID {
		return fmt.Errorf("assessment weights default_profile_id %s is reserved", generalProfileID)
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
		if profile.ID == generalProfileID {
			return fmt.Errorf("assessment weights profile id %s is reserved", generalProfileID)
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

func hasTreeCustomWeights(req EvaluateRequest) bool {
	return len(req.GroupWeightsPercent) > 0 ||
		len(req.SectionWeightsPercent) > 0 ||
		len(req.SubsectionWeightsPercent) > 0 ||
		len(req.IndicatorWeightsPercent) > 0
}

func (s *Service) assessmentTree() []AssessmentGroupConfig {
	groups := make([]AssessmentGroupConfig, 0)
	groupIndex := make(map[string]int)
	sectionIndex := make(map[string]map[string]int)
	subsectionIndex := make(map[string]map[string]map[string]int)
	indicatorSeen := make(map[string]struct{}, len(s.config.Indicators))

	for _, indicator := range s.config.Indicators {
		groupPos, ok := groupIndex[indicator.GroupID]
		if !ok {
			groupPos = len(groups)
			groupIndex[indicator.GroupID] = groupPos
			sectionIndex[indicator.GroupID] = make(map[string]int)
			subsectionIndex[indicator.GroupID] = make(map[string]map[string]int)
			groups = append(groups, AssessmentGroupConfig{
				ID:    indicator.GroupID,
				Title: indicator.GroupTitle,
			})
		}

		sectionPos, ok := sectionIndex[indicator.GroupID][indicator.SectionID]
		if !ok {
			sectionPos = len(groups[groupPos].Sections)
			sectionIndex[indicator.GroupID][indicator.SectionID] = sectionPos
			subsectionIndex[indicator.GroupID][indicator.SectionID] = make(map[string]int)
			groups[groupPos].Sections = append(groups[groupPos].Sections, AssessmentSectionConfig{
				ID:    indicator.SectionID,
				Title: indicator.SectionTitle,
			})
		}

		subsectionPos, ok := subsectionIndex[indicator.GroupID][indicator.SectionID][indicator.SubsectionID]
		if !ok {
			subsectionPos = len(groups[groupPos].Sections[sectionPos].Subsections)
			subsectionIndex[indicator.GroupID][indicator.SectionID][indicator.SubsectionID] = subsectionPos
			groups[groupPos].Sections[sectionPos].Subsections = append(groups[groupPos].Sections[sectionPos].Subsections, AssessmentSubsectionConfig{
				ID:    indicator.SubsectionID,
				Title: indicator.SubsectionTitle,
			})
		}

		if _, ok := indicatorSeen[indicator.ID]; ok {
			continue
		}
		indicatorSeen[indicator.ID] = struct{}{}
		subsections := groups[groupPos].Sections[sectionPos].Subsections
		subsections[subsectionPos].Indicators = append(subsections[subsectionPos].Indicators, AssessmentIndicatorConfig{
			ID:    indicator.ID,
			Title: indicator.Title,
		})
		groups[groupPos].Sections[sectionPos].Subsections = subsections
	}

	return groups
}

func (s *Service) evaluationWeights(profile ProfileWeights) evaluationWeights {
	weights := evaluationWeights{
		ID:                profile.ID,
		Title:             profile.Title,
		GroupWeights:      cloneWeightMap(profile.GroupWeights),
		SectionWeights:    cloneNestedWeightMap(profile.SectionWeights),
		SubsectionWeights: make(map[string]map[string]float64),
		IndicatorWeights:  make(map[string]map[string]float64),
	}

	sectionIndicatorWeights := make(map[string]map[string]float64)
	for sectionID, values := range s.weights.DefaultIndicatorWeights {
		sectionIndicatorWeights[sectionID] = cloneWeightMap(values)
	}
	for sectionID, values := range profile.IndicatorWeights {
		sectionIndicatorWeights[sectionID] = cloneWeightMap(values)
	}

	for _, indicator := range s.config.Indicators {
		sectionWeights := sectionIndicatorWeights[indicator.SectionID]
		weight := 0.0
		if sectionWeights != nil {
			weight = sectionWeights[indicator.ID]
		}
		if weights.SubsectionWeights[indicator.SectionID] == nil {
			weights.SubsectionWeights[indicator.SectionID] = make(map[string]float64)
		}
		weights.SubsectionWeights[indicator.SectionID][indicator.SubsectionID] += weight
	}

	for _, indicator := range s.config.Indicators {
		if weights.IndicatorWeights[indicator.SubsectionID] == nil {
			weights.IndicatorWeights[indicator.SubsectionID] = make(map[string]float64)
		}
		sectionWeights := sectionIndicatorWeights[indicator.SectionID]
		subsectionTotal := weights.SubsectionWeights[indicator.SectionID][indicator.SubsectionID]
		if sectionWeights == nil || subsectionTotal <= 0 {
			weights.IndicatorWeights[indicator.SubsectionID][indicator.ID] = 0
			continue
		}
		weights.IndicatorWeights[indicator.SubsectionID][indicator.ID] = sectionWeights[indicator.ID] / subsectionTotal
	}

	return weights
}

func (s *Service) generalEvaluationWeights() evaluationWeights {
	profiles := make([]evaluationWeights, 0, len(s.weights.Profiles))
	for _, profile := range s.weights.Profiles {
		profiles = append(profiles, s.evaluationWeights(profile))
	}

	return evaluationWeights{
		ID:                generalProfileID,
		Title:             generalProfileTitle,
		GroupWeights:      averageWeightMap(extractWeightMaps(profiles, func(item evaluationWeights) map[string]float64 { return item.GroupWeights })),
		SectionWeights:    averageNestedWeightMap(extractNestedWeightMaps(profiles, func(item evaluationWeights) map[string]map[string]float64 { return item.SectionWeights })),
		SubsectionWeights: averageNestedWeightMap(extractNestedWeightMaps(profiles, func(item evaluationWeights) map[string]map[string]float64 { return item.SubsectionWeights })),
		IndicatorWeights:  averageNestedWeightMap(extractNestedWeightMaps(profiles, func(item evaluationWeights) map[string]map[string]float64 { return item.IndicatorWeights })),
	}
}

func (s *Service) defaultGeneralProfileWeights() map[string]float64 {
	result := make(map[string]float64, len(s.weights.Profiles))
	if len(s.weights.Profiles) == 0 {
		return result
	}

	weight := 1.0 / float64(len(s.weights.Profiles))
	for _, profile := range s.weights.Profiles {
		result[profile.ID] = weight
	}
	return result
}

func extractWeightMaps(items []evaluationWeights, pick func(evaluationWeights) map[string]float64) []map[string]float64 {
	result := make([]map[string]float64, 0, len(items))
	for _, item := range items {
		result = append(result, pick(item))
	}
	return result
}

func extractNestedWeightMaps(items []evaluationWeights, pick func(evaluationWeights) map[string]map[string]float64) []map[string]map[string]float64 {
	result := make([]map[string]map[string]float64, 0, len(items))
	for _, item := range items {
		result = append(result, pick(item))
	}
	return result
}

func averageWeightMap(items []map[string]float64) map[string]float64 {
	result := make(map[string]float64)
	if len(items) == 0 {
		return result
	}

	for _, item := range items {
		for key, value := range item {
			result[key] += value
		}
	}
	for key := range result {
		result[key] /= float64(len(items))
	}

	return result
}

func averageNestedWeightMap(items []map[string]map[string]float64) map[string]map[string]float64 {
	result := make(map[string]map[string]float64)
	if len(items) == 0 {
		return result
	}

	for _, item := range items {
		for parentID, values := range item {
			if result[parentID] == nil {
				result[parentID] = make(map[string]float64)
			}
			for childID, value := range values {
				result[parentID][childID] += value
			}
		}
	}
	for parentID := range result {
		for childID := range result[parentID] {
			result[parentID][childID] /= float64(len(items))
		}
	}

	return result
}

func indicatorsWithWeights(indicators []IndicatorResult, weights evaluationWeights) []IndicatorResult {
	result := make([]IndicatorResult, 0, len(indicators))
	for _, indicator := range indicators {
		indicator.Weight = indicatorWeight(weights, indicator.SubsectionID, indicator.ID)
		result = append(result, indicator)
	}
	return result
}

func cloneWeightMap(weights map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(weights))
	for key, value := range weights {
		result[key] = value
	}
	return result
}

func cloneNestedWeightMap(weights map[string]map[string]float64) map[string]map[string]float64 {
	result := make(map[string]map[string]float64, len(weights))
	for key, values := range weights {
		result[key] = cloneWeightMap(values)
	}
	return result
}

func percentMap(weights map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(weights))
	for key, value := range weights {
		result[key] = round(value*100, 4)
	}
	return result
}

func nestedPercentMap(weights map[string]map[string]float64) map[string]map[string]float64 {
	result := make(map[string]map[string]float64, len(weights))
	for key, values := range weights {
		result[key] = percentMap(values)
	}
	return result
}

func customPercentWeights(name string, percentWeights map[string]float64, expected map[string]float64) (map[string]float64, error) {
	if len(percentWeights) != len(expected) {
		return nil, RequestError{Message: name + " must include all items"}
	}

	result := make(map[string]float64, len(expected))
	sum := 0.0
	for id := range expected {
		percent, ok := percentWeights[id]
		if !ok {
			return nil, RequestError{Message: name + " must include all items"}
		}
		if math.IsNaN(percent) || math.IsInf(percent, 0) {
			return nil, RequestError{Message: name + " must be finite numbers"}
		}
		if percent < 0 {
			return nil, RequestError{Message: name + " must be non-negative"}
		}
		sum += percent
		result[id] = percent / 100
	}
	for id := range percentWeights {
		if _, ok := expected[id]; !ok {
			return nil, RequestError{Message: name + " contain unknown items"}
		}
	}
	if math.Abs(sum-100) > 0.001 {
		return nil, RequestError{Message: name + " must sum to 100"}
	}

	return result, nil
}

func customNestedPercentWeights(name string, percentWeights map[string]map[string]float64, expected map[string]map[string]float64) (map[string]map[string]float64, error) {
	if len(percentWeights) != len(expected) {
		return nil, RequestError{Message: name + " must include all parent items"}
	}

	result := make(map[string]map[string]float64, len(expected))
	for parentID, expectedChildren := range expected {
		children, ok := percentWeights[parentID]
		if !ok {
			return nil, RequestError{Message: name + " must include all parent items"}
		}
		custom, err := customPercentWeights(name+"."+parentID, children, expectedChildren)
		if err != nil {
			return nil, err
		}
		result[parentID] = custom
	}
	for parentID := range percentWeights {
		if _, ok := expected[parentID]; !ok {
			return nil, RequestError{Message: name + " contain unknown parent items"}
		}
	}

	return result, nil
}

func groupWeight(weights evaluationWeights, groupID string) float64 {
	return weights.GroupWeights[groupID]
}

func sectionWeight(weights evaluationWeights, groupID string, sectionID string) float64 {
	groupWeights := weights.SectionWeights[groupID]
	if groupWeights == nil {
		return 0
	}
	return groupWeights[sectionID]
}

func subsectionWeight(weights evaluationWeights, sectionID string, subsectionID string) float64 {
	sectionWeights := weights.SubsectionWeights[sectionID]
	if sectionWeights == nil {
		return 0
	}
	return sectionWeights[subsectionID]
}

func indicatorWeight(weights evaluationWeights, subsectionID string, indicatorID string) float64 {
	subsectionWeights := weights.IndicatorWeights[subsectionID]
	if subsectionWeights == nil {
		return 0
	}
	return subsectionWeights[indicatorID]
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
