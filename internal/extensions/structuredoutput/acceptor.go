package structuredoutput

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

type Acceptor struct {
	config    Config
	configErr error
}

func NewFromEnvironment() *Acceptor {
	return NewFromLookup(os.Getenv)
}

func NewFromLookup(lookup envLookup) *Acceptor {
	cfg, err := ConfigFromLookup(lookup)
	if err != nil && !validMode(cfg.Mode) {
		// An unknown mode must never accidentally enable mutation. Other bad
		// settings preserve a valid requested mode: shadow stays observational,
		// while enforce still fails closed when the first hook is invoked.
		cfg.Mode = port.ModeOff
	}
	return &Acceptor{config: cfg, configErr: err}
}

func (a *Acceptor) Mode() port.Mode {
	if a == nil {
		return port.ModeOff
	}
	return a.config.Mode
}

func (a *Acceptor) CallTimeout() time.Duration {
	if a == nil {
		return 0
	}
	return a.config.LLMTimeout
}

func (a *Acceptor) Accept(ctx context.Context, req port.Request) (port.Result, error) {
	policy := a.config.policy(req.ModelID)
	metadata := metadataFor(a.Mode(), policy, req.Raw)
	if a.configErr != nil {
		err := contractError(port.ErrorConfiguration, req.Contract, a.configErr)
		a.logFailure(ctx, metadata, req.Contract, err)
		return port.Result{JSON: req.Raw, Metadata: metadata}, err
	}

	decoded, err := decodeJSON(req.Raw, policy.Acceptance)
	if err != nil {
		code := classifyDecodeError(req.Raw, err)
		contractErr := contractError(code, req.Contract, err)
		a.logFailure(ctx, metadata, req.Contract, contractErr)
		return port.Result{JSON: req.Raw, Metadata: metadata}, contractErr
	}
	metadata.Strategy = decoded.strategy
	metadata.Repairs = append([]string(nil), decoded.repairs...)

	normalized, changes, err := normalizeContract(decoded.value, req)
	metadata.Normalizations = changes
	if err != nil {
		contractErr := contractError(port.ErrorJSONSemantic, req.Contract, err)
		a.logFailure(ctx, metadata, req.Contract, contractErr)
		return port.Result{JSON: req.Raw, Metadata: metadata}, contractErr
	}

	output := decoded.source
	if changes > 0 || decoded.strategy != "direct" || len(decoded.repairs) > 0 {
		encoded, marshalErr := json.Marshal(normalized)
		if marshalErr != nil {
			contractErr := contractError(port.ErrorJSONSchema, req.Contract, marshalErr)
			a.logFailure(ctx, metadata, req.Contract, contractErr)
			return port.Result{JSON: req.Raw, Metadata: metadata}, contractErr
		}
		output = string(encoded)
	}

	a.logSuccess(ctx, metadata, req.Contract)
	return port.Result{JSON: output, Metadata: metadata}, nil
}

func (a *Acceptor) ValidateResponse(ctx context.Context, response port.Response) error {
	policy := a.config.policy(response.ModelID)
	metadata := metadataFor(a.Mode(), policy, response.Content)
	if a.configErr != nil {
		err := contractError(port.ErrorConfiguration, response.Contract, a.configErr)
		a.logFailure(ctx, metadata, response.Contract, err)
		return err
	}
	if strings.TrimSpace(response.Content) == "" {
		err := contractError(port.ErrorEmptyContent, response.Contract, errors.New("model returned blank content"))
		a.logFailure(ctx, metadata, response.Contract, err)
		return err
	}
	finishReason := strings.ToLower(strings.TrimSpace(response.FinishReason))
	if finishReason == "length" || finishReason == "max_tokens" || finishReason == "max_completion_tokens" {
		err := contractError(port.ErrorJSONTruncated, response.Contract, fmt.Errorf("finish_reason=%s", finishReason))
		a.logFailure(ctx, metadata, response.Contract, err)
		return err
	}
	return nil
}

func normalizeContract(value any, req port.Request) (any, int, error) {
	switch req.Contract {
	case port.ContractWikiCombined:
		return normalizeWikiCombined(value)
	case port.ContractWikiCitation:
		return normalizeWikiCitation(value, req)
	case port.ContractWikiDedup:
		return normalizeWikiDedup(value)
	case port.ContractWikiTaxonomy:
		return normalizeWikiTaxonomy(value, req)
	case port.ContractGraphDocument:
		return normalizeGraphDocument(value)
	case port.ContractGraphLegacyEntities:
		return normalizeLegacyEntities(value)
	case port.ContractGraphLegacyRelations:
		return normalizeLegacyRelations(value)
	default:
		return nil, 0, fmt.Errorf("unsupported structured-output contract %q", req.Contract)
	}
}

func metadataFor(mode port.Mode, policy ModelRule, raw string) port.Metadata {
	digest := sha256.Sum256([]byte(raw))
	return port.Metadata{
		Mode:            mode,
		Acceptance:      policy.Acceptance,
		GenerationOwner: policy.GenerationOwner,
		RawCharacters:   utf8.RuneCountInString(raw),
		RawSHA256:       hex.EncodeToString(digest[:]),
	}
}

func classifyDecodeError(raw string, err error) port.ErrorCode {
	if strings.TrimSpace(raw) == "" || errors.Is(err, io.EOF) {
		return port.ErrorJSONEmpty
	}
	lower := strings.ToLower(err.Error())
	if errors.Is(err, io.ErrUnexpectedEOF) || strings.Contains(lower, "unexpected eof") || strings.Contains(lower, "unexpected end") {
		return port.ErrorJSONTruncated
	}
	return port.ErrorJSONSyntax
}

func contractError(code port.ErrorCode, contract port.Contract, err error) error {
	return &port.ContractError{Code: code, Contract: contract, Err: err}
}

func (a *Acceptor) logSuccess(ctx context.Context, metadata port.Metadata, contract port.Contract) {
	logger.Infof(ctx,
		"structured output accepted contract=%s mode=%s acceptance=%s generation_owner=%s strategy=%s repairs=%v normalizations=%d raw_chars=%d raw_sha256=%s",
		contract, metadata.Mode, metadata.Acceptance, metadata.GenerationOwner, metadata.Strategy,
		metadata.Repairs, metadata.Normalizations, metadata.RawCharacters, metadata.RawSHA256,
	)
}

func (a *Acceptor) logFailure(ctx context.Context, metadata port.Metadata, contract port.Contract, err error) {
	logger.Warnf(ctx,
		"structured output rejected contract=%s mode=%s acceptance=%s generation_owner=%s strategy=%s repairs=%v normalizations=%d raw_chars=%d raw_sha256=%s error=%v",
		contract, metadata.Mode, metadata.Acceptance, metadata.GenerationOwner, metadata.Strategy,
		metadata.Repairs, metadata.Normalizations, metadata.RawCharacters, metadata.RawSHA256, err,
	)
}
