// Package structuredoutput defines the small application-facing port used by
// the optional structured-output extension. The default implementation is a
// no-op, so normal WeKnora builds keep the original parsing path unchanged.
package structuredoutput

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModeShadow  Mode = "shadow"
	ModeEnforce Mode = "enforce"
)

type Acceptance string

const (
	AcceptanceStrict        Acceptance = "strict"
	AcceptanceCompatibility Acceptance = "compatibility"
)

type GenerationOwner string

const (
	GenerationOwnerNone    GenerationOwner = "none"
	GenerationOwnerGateway GenerationOwner = "gateway"
	GenerationOwnerWeKnora GenerationOwner = "weknora"
)

type Contract string

const (
	ContractWikiCombined         Contract = "wiki.combined_extraction"
	ContractWikiCitation         Contract = "wiki.chunk_citation"
	ContractWikiDedup            Contract = "wiki.deduplication"
	ContractWikiTaxonomy         Contract = "wiki.taxonomy"
	ContractGraphDocument        Contract = "graph.document"
	ContractGraphLegacyEntities  Contract = "graph.legacy_entities"
	ContractGraphLegacyRelations Contract = "graph.legacy_relations"
)

type Candidate struct {
	Slug string
	Kind string
}

// Request contains only invocation-local contract data. It deliberately has
// no repository or persistence dependencies.
type Request struct {
	Contract   Contract
	Raw        string
	ModelID    string
	Candidates []Candidate
	Handles    []string
}

type Response struct {
	Contract     Contract
	Content      string
	FinishReason string
	ModelID      string
}

type Metadata struct {
	Mode            Mode
	Acceptance      Acceptance
	GenerationOwner GenerationOwner
	Strategy        string
	Repairs         []string
	Normalizations  int
	RawCharacters   int
	RawSHA256       string
}

type Result struct {
	JSON     string
	Metadata Metadata
}

type ErrorCode string

const (
	ErrorJSONEmpty     ErrorCode = "LLM_JSON_EMPTY"
	ErrorJSONSyntax    ErrorCode = "LLM_JSON_SYNTAX"
	ErrorJSONTruncated ErrorCode = "LLM_JSON_TRUNCATED"
	ErrorJSONSchema    ErrorCode = "LLM_JSON_SCHEMA"
	ErrorJSONSemantic  ErrorCode = "LLM_JSON_SEMANTIC"
	ErrorEmptyContent  ErrorCode = "LLM_EMPTY_CONTENT"
	ErrorConfiguration ErrorCode = "LLM_STRUCTURED_OUTPUT_CONFIG"
)

type ContractError struct {
	Code     ErrorCode
	Contract Contract
	Err      error
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Contract)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Contract, e.Err)
}

func (e *ContractError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Acceptor interface {
	Mode() Mode
	Accept(context.Context, Request) (Result, error)
	ValidateResponse(context.Context, Response) error
	CallTimeout() time.Duration
}

type passthrough struct{}

func (passthrough) Mode() Mode { return ModeOff }

func (passthrough) Accept(_ context.Context, req Request) (Result, error) {
	return Result{JSON: req.Raw, Metadata: Metadata{Mode: ModeOff}}, nil
}

func (passthrough) ValidateResponse(context.Context, Response) error { return nil }
func (passthrough) CallTimeout() time.Duration                       { return 0 }

var registry = struct {
	sync.RWMutex
	acceptor Acceptor
}{acceptor: passthrough{}}

// Register replaces the process-wide extension implementation. It returns a
// restore closure so focused tests can safely undo registration.
func Register(acceptor Acceptor) func() {
	if acceptor == nil {
		acceptor = passthrough{}
	}
	registry.Lock()
	previous := registry.acceptor
	registry.acceptor = acceptor
	registry.Unlock()
	return func() {
		registry.Lock()
		registry.acceptor = previous
		registry.Unlock()
	}
}

func current() Acceptor {
	registry.RLock()
	acceptor := registry.acceptor
	registry.RUnlock()
	return acceptor
}

func CurrentMode() Mode { return current().Mode() }
func Enabled() bool     { return CurrentMode() != ModeOff }

// Accept keeps shadow mode observational even if an implementation
// accidentally returns rewritten output or a validation error.
func Accept(ctx context.Context, req Request) (Result, error) {
	acceptor := current()
	if acceptor.Mode() == ModeOff {
		return Result{JSON: req.Raw, Metadata: Metadata{Mode: ModeOff}}, nil
	}
	result, err := acceptor.Accept(ctx, req)
	if acceptor.Mode() == ModeShadow {
		result.JSON = req.Raw
		return result, nil
	}
	return result, err
}

func ValidateResponse(ctx context.Context, response Response) error {
	acceptor := current()
	if acceptor.Mode() == ModeOff {
		return nil
	}
	err := acceptor.ValidateResponse(ctx, response)
	if acceptor.Mode() == ModeShadow {
		return nil
	}
	return err
}

// WithCallTimeout applies the extension's per-call deadline only in enforce
// mode. Off and shadow must preserve the original call context exactly.
func WithCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	acceptor := current()
	if acceptor.Mode() != ModeEnforce || acceptor.CallTimeout() <= 0 {
		return ctx, func() {}
	}
	timeout := acceptor.CallTimeout()
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}
