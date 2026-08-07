package structuredoutput

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

func enforceAcceptor(acceptance port.Acceptance) *Acceptor {
	return &Acceptor{config: Config{
		Mode:                   port.ModeEnforce,
		DefaultAcceptance:      acceptance,
		DefaultGenerationOwner: port.GenerationOwnerNone,
		ModelRules:             map[string]ModelRule{},
		LLMTimeout:             5 * time.Minute,
	}}
}

func acceptJSON(t *testing.T, acceptor *Acceptor, req port.Request) any {
	t.Helper()
	result, err := acceptor.Accept(context.Background(), req)
	if err != nil {
		t.Fatalf("Accept() returned %v", err)
	}
	var decoded any
	if err := json.Unmarshal([]byte(result.JSON), &decoded); err != nil {
		t.Fatalf("accepted output is not JSON: %v; output=%q", err, result.JSON)
	}
	return decoded
}

func requireContractCode(t *testing.T, err error, code port.ErrorCode) {
	t.Helper()
	var contractErr *port.ContractError
	if !errors.As(err, &contractErr) {
		t.Fatalf("error %v is not a ContractError", err)
	}
	if contractErr.Code != code {
		t.Fatalf("ContractError code = %s, want %s", contractErr.Code, code)
	}
}

func TestStrictAcceptsCanonicalJSONWithoutRewrite(t *testing.T) {
	acceptor := enforceAcceptor(port.AcceptanceStrict)
	raw := `{"entities":[],"concepts":[]}`
	result, err := acceptor.Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCombined,
		Raw:      raw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.JSON != raw {
		t.Fatalf("canonical JSON changed: got %q want %q", result.JSON, raw)
	}
}

func TestStrictRejectsFenceAndCompatibilityRecoversIt(t *testing.T) {
	raw := "```json\n{\"entities\":[],\"concepts\":[]}\n```"
	_, err := enforceAcceptor(port.AcceptanceStrict).Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCombined,
		Raw:      raw,
	})
	requireContractCode(t, err, port.ErrorJSONSyntax)

	acceptJSON(t, enforceAcceptor(port.AcceptanceCompatibility), port.Request{
		Contract: port.ContractWikiCombined,
		Raw:      raw,
	})
}

func TestCompatibilityRecoversBoundedLexicalDefects(t *testing.T) {
	tests := []string{
		"model note: {\"entities\":[],\"concepts\":[]} done",
		"{\"entities\":[],\"concepts\":[],}",
		"{\"entities\":[],\"concepts\":[],\"note\":\"C:\\bad\\q\"}",
	}
	acceptor := enforceAcceptor(port.AcceptanceCompatibility)
	for _, raw := range tests {
		acceptJSON(t, acceptor, port.Request{Contract: port.ContractWikiCombined, Raw: raw})
	}
}

func TestCompatibilityDoesNotCompleteTruncatedJSON(t *testing.T) {
	_, err := enforceAcceptor(port.AcceptanceCompatibility).Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCombined,
		Raw:      `{"entities":[],"concepts":[`,
	})
	requireContractCode(t, err, port.ErrorJSONTruncated)
}

func TestWikiCombinedNormalizesOnlyProvableSlugNamespace(t *testing.T) {
	decoded := acceptJSON(t, enforceAcceptor(port.AcceptanceStrict), port.Request{
		Contract: port.ContractWikiCombined,
		Raw: `{"entities":[{"name":"Alice","slug":"alice"}],` +
			`"concepts":[{"name":"Fusion","slug":"fusion"}]}`,
	}).(map[string]any)
	entities := decoded["entities"].([]any)
	concepts := decoded["concepts"].([]any)
	if entities[0].(map[string]any)["slug"] != "entity/alice" {
		t.Fatalf("entity slug was not canonicalized: %v", entities)
	}
	if concepts[0].(map[string]any)["slug"] != "concept/fusion" {
		t.Fatalf("concept slug was not canonicalized: %v", concepts)
	}

	_, err := enforceAcceptor(port.AcceptanceStrict).Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCombined,
		Raw:      `{"entities":[{"name":"Alice","slug":"concept/alice"}],"concepts":[]}`,
	})
	requireContractCode(t, err, port.ErrorJSONSemantic)
}

func TestWikiCitationResolvesUniqueBareSlugAndValidatesHandles(t *testing.T) {
	decoded := acceptJSON(t, enforceAcceptor(port.AcceptanceStrict), port.Request{
		Contract: port.ContractWikiCitation,
		Raw:      `{"citations":{"fusion":["c000","c000"]},"new_slugs":[]}`,
		Candidates: []port.Candidate{
			{Slug: "concept/fusion", Kind: "concept"},
		},
		Handles: []string{"c000"},
	}).(map[string]any)
	citations := decoded["citations"].(map[string]any)
	values, ok := citations["concept/fusion"].([]any)
	if !ok || len(values) != 1 || values[0] != "c000" {
		t.Fatalf("unexpected normalized citations: %v", citations)
	}

	_, err := enforceAcceptor(port.AcceptanceStrict).Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCitation,
		Raw:      `{"citations":{"fusion":["unknown"]},"new_slugs":[]}`,
		Candidates: []port.Candidate{
			{Slug: "concept/fusion", Kind: "concept"},
		},
		Handles: []string{"c000"},
	})
	requireContractCode(t, err, port.ErrorJSONSemantic)
}

func TestWikiCitationRejectsAmbiguousBareSlug(t *testing.T) {
	_, err := enforceAcceptor(port.AcceptanceStrict).Accept(context.Background(), port.Request{
		Contract: port.ContractWikiCitation,
		Raw:      `{"citations":{"shared":["c000"]},"new_slugs":[]}`,
		Candidates: []port.Candidate{
			{Slug: "entity/shared", Kind: "entity"},
			{Slug: "concept/shared", Kind: "concept"},
		},
		Handles: []string{"c000"},
	})
	requireContractCode(t, err, port.ErrorJSONSemantic)
}

func TestGraphCanonicalAndKnownAliasesNormalizeToLegacyShape(t *testing.T) {
	canonical := acceptJSON(t, enforceAcceptor(port.AcceptanceStrict), port.Request{
		Contract: port.ContractGraphDocument,
		Raw: `{"nodes":[{"name":"Alice","attributes":["person"]}],` +
			`"relations":[{"source":"Alice","target":"Bob","type":"knows"}]}`,
	}).([]any)
	if len(canonical) != 2 {
		t.Fatalf("canonical graph produced %d groups, want 2: %v", len(canonical), canonical)
	}

	aliases := acceptJSON(t, enforceAcceptor(port.AcceptanceStrict), port.Request{
		Contract: port.ContractGraphDocument,
		Raw:      `[{"subject":{"name":"Alice"},"object":{"text":"Bob"},"predicate":"knows"}]`,
	}).([]any)
	relation := aliases[0].(map[string]any)
	if relation["entity1"] != "Alice" || relation["entity2"] != "Bob" || relation["relation"] != "knows" {
		t.Fatalf("unexpected normalized relation: %v", relation)
	}
}

func TestGraphRejectsUnsupportedNonEmptyShape(t *testing.T) {
	_, err := enforceAcceptor(port.AcceptanceStrict).Accept(context.Background(), port.Request{
		Contract: port.ContractGraphDocument,
		Raw:      `{"answer":"Alice knows Bob"}`,
	})
	requireContractCode(t, err, port.ErrorJSONSemantic)
}

func TestGraphNodeTypeFieldIsNotMisclassifiedAsRelation(t *testing.T) {
	groups := acceptJSON(t, enforceAcceptor(port.AcceptanceStrict), port.Request{
		Contract: port.ContractGraphDocument,
		Raw:      `[{"entity":"Alice","type":"person"}]`,
	}).([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["entity"] != "Alice" {
		t.Fatalf("unexpected normalized node: %v", groups)
	}
}

func TestResponseGuardRejectsBlankAndLength(t *testing.T) {
	acceptor := enforceAcceptor(port.AcceptanceStrict)
	err := acceptor.ValidateResponse(context.Background(), port.Response{
		Contract: port.ContractWikiCombined,
		Content:  "  ",
	})
	requireContractCode(t, err, port.ErrorEmptyContent)

	err = acceptor.ValidateResponse(context.Background(), port.Response{
		Contract:     port.ContractGraphDocument,
		Content:      `{}`,
		FinishReason: "length",
	})
	requireContractCode(t, err, port.ErrorJSONTruncated)
}

func TestPerModelRuleOverridesDefaults(t *testing.T) {
	values := map[string]string{
		envMode:            "enforce",
		envAcceptance:      "compatibility",
		envGenerationOwner: "none",
		envModelRules:      `{"gateway-model":{"acceptance":"strict","generation_owner":"gateway"}}`,
		envTimeoutSeconds:  "90",
	}
	acceptor := NewFromLookup(func(key string) string { return values[key] })
	if acceptor.Mode() != port.ModeEnforce || acceptor.CallTimeout() != 90*time.Second {
		t.Fatalf("unexpected config: mode=%s timeout=%s", acceptor.Mode(), acceptor.CallTimeout())
	}
	policy := acceptor.config.policy("gateway-model")
	if policy.Acceptance != port.AcceptanceStrict || policy.GenerationOwner != port.GenerationOwnerGateway {
		t.Fatalf("unexpected gateway policy: %+v", policy)
	}
}

func TestInvalidShadowConfigCannotEscalateToEnforce(t *testing.T) {
	acceptor := NewFromLookup(func(key string) string {
		switch key {
		case envMode:
			return "shadow"
		case envAcceptance:
			return "invalid"
		default:
			return ""
		}
	})
	if acceptor.Mode() != port.ModeShadow {
		t.Fatalf("invalid shadow config escalated mode to %s", acceptor.Mode())
	}
}

func TestUnknownModeFallsBackToOff(t *testing.T) {
	acceptor := NewFromLookup(func(key string) string {
		if key == envMode {
			return "unknown"
		}
		return ""
	})
	if acceptor.Mode() != port.ModeOff {
		t.Fatalf("unknown mode resolved to %s, want off", acceptor.Mode())
	}
}
