package structuredoutput

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

const (
	envMode            = "WEKNORA_STRUCTURED_OUTPUT_MODE"
	envAcceptance      = "WEKNORA_STRUCTURED_OUTPUT_ACCEPTANCE"
	envGenerationOwner = "WEKNORA_STRUCTURED_OUTPUT_GENERATION_OWNER"
	envModelRules      = "WEKNORA_STRUCTURED_OUTPUT_MODEL_RULES"
	envTimeoutSeconds  = "WEKNORA_STRUCTURED_OUTPUT_LLM_TIMEOUT_SECONDS"
)

type ModelRule struct {
	Acceptance      port.Acceptance      `json:"acceptance"`
	GenerationOwner port.GenerationOwner `json:"generation_owner"`
}

type Config struct {
	Mode                   port.Mode
	DefaultAcceptance      port.Acceptance
	DefaultGenerationOwner port.GenerationOwner
	ModelRules             map[string]ModelRule
	LLMTimeout             time.Duration
}

type envLookup func(string) string

func ConfigFromLookup(lookup envLookup) (Config, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}

	cfg := Config{
		Mode:                   port.ModeOff,
		DefaultAcceptance:      port.AcceptanceStrict,
		DefaultGenerationOwner: port.GenerationOwnerNone,
		ModelRules:             map[string]ModelRule{},
		LLMTimeout:             5 * time.Minute,
	}

	if raw := strings.TrimSpace(lookup(envMode)); raw != "" {
		cfg.Mode = port.Mode(strings.ToLower(raw))
	}
	if !validMode(cfg.Mode) {
		return cfg, fmt.Errorf("%s must be off, shadow, or enforce", envMode)
	}

	if raw := strings.TrimSpace(lookup(envAcceptance)); raw != "" {
		cfg.DefaultAcceptance = port.Acceptance(strings.ToLower(raw))
	}
	if !validAcceptance(cfg.DefaultAcceptance) {
		return cfg, fmt.Errorf("%s must be strict or compatibility", envAcceptance)
	}

	if raw := strings.TrimSpace(lookup(envGenerationOwner)); raw != "" {
		cfg.DefaultGenerationOwner = port.GenerationOwner(strings.ToLower(raw))
	}
	if !validGenerationOwner(cfg.DefaultGenerationOwner) {
		return cfg, fmt.Errorf("%s must be none, gateway, or weknora", envGenerationOwner)
	}

	if raw := strings.TrimSpace(lookup(envTimeoutSeconds)); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return cfg, fmt.Errorf("%s must be a positive integer", envTimeoutSeconds)
		}
		cfg.LLMTimeout = time.Duration(seconds) * time.Second
	}

	if raw := strings.TrimSpace(lookup(envModelRules)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg.ModelRules); err != nil {
			return cfg, fmt.Errorf("parse %s: %w", envModelRules, err)
		}
		normalized := make(map[string]ModelRule, len(cfg.ModelRules))
		for modelID, rule := range cfg.ModelRules {
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				return cfg, fmt.Errorf("%s contains a blank model id", envModelRules)
			}
			if rule.Acceptance == "" {
				rule.Acceptance = cfg.DefaultAcceptance
			}
			if rule.GenerationOwner == "" {
				rule.GenerationOwner = cfg.DefaultGenerationOwner
			}
			if !validAcceptance(rule.Acceptance) {
				return cfg, fmt.Errorf("model %s has invalid acceptance %q", modelID, rule.Acceptance)
			}
			if !validGenerationOwner(rule.GenerationOwner) {
				return cfg, fmt.Errorf("model %s has invalid generation_owner %q", modelID, rule.GenerationOwner)
			}
			normalized[modelID] = rule
		}
		cfg.ModelRules = normalized
	}

	return cfg, nil
}

func validMode(mode port.Mode) bool {
	return mode == port.ModeOff || mode == port.ModeShadow || mode == port.ModeEnforce
}

func validAcceptance(acceptance port.Acceptance) bool {
	return acceptance == port.AcceptanceStrict || acceptance == port.AcceptanceCompatibility
}

func validGenerationOwner(owner port.GenerationOwner) bool {
	return owner == port.GenerationOwnerNone || owner == port.GenerationOwnerGateway || owner == port.GenerationOwnerWeKnora
}

func (c Config) policy(modelID string) ModelRule {
	if rule, ok := c.ModelRules[strings.TrimSpace(modelID)]; ok {
		return rule
	}
	return ModelRule{
		Acceptance:      c.DefaultAcceptance,
		GenerationOwner: c.DefaultGenerationOwner,
	}
}
