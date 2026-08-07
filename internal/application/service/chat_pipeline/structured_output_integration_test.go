//go:build weknora_structured_output

package chatpipeline

import (
	"context"
	"testing"

	extension "github.com/Tencent/WeKnora/internal/extensions/structuredoutput"
	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

func TestStructuredOutputGraphFeedsOriginalFormater(t *testing.T) {
	values := map[string]string{
		"WEKNORA_STRUCTURED_OUTPUT_MODE":       "enforce",
		"WEKNORA_STRUCTURED_OUTPUT_ACCEPTANCE": "strict",
	}
	acceptor := extension.NewFromLookup(func(key string) string { return values[key] })
	result, err := acceptor.Accept(context.Background(), port.Request{
		Contract: port.ContractGraphDocument,
		Raw: `{"nodes":[{"name":"Alice","attributes":["person"]}],` +
			`"relations":[{"subject":"Alice","object":"Bob","predicate":"knows"}]}`,
	})
	if err != nil {
		t.Fatalf("structured-output acceptance failed: %v", err)
	}

	graph, err := NewFormater().ParseGraph(context.Background(), result.JSON)
	if err != nil {
		t.Fatalf("original Formater.ParseGraph rejected normalized JSON: %v", err)
	}
	if len(graph.Node) != 2 {
		t.Fatalf("graph has %d nodes, want 2: %+v", len(graph.Node), graph.Node)
	}
	if len(graph.Relation) != 1 {
		t.Fatalf("graph has %d relations, want 1: %+v", len(graph.Relation), graph.Relation)
	}
	relation := graph.Relation[0]
	if relation.Node1 != "Alice" || relation.Node2 != "Bob" || relation.Type != "knows" {
		t.Fatalf("unexpected graph relation: %+v", relation)
	}
}
