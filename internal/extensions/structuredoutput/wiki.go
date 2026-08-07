package structuredoutput

import (
	"fmt"
	"strings"

	port "github.com/Tencent/WeKnora/internal/structuredoutput"
)

func normalizeWikiCombined(value any) (any, int, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("combined extraction must be an object")
	}
	changes := 0
	for _, spec := range []struct {
		field string
		kind  string
	}{{field: "entities", kind: "entity"}, {field: "concepts", kind: "concept"}} {
		items, exists := root[spec.field]
		if !exists {
			return nil, changes, fmt.Errorf("missing required field %q", spec.field)
		}
		list, ok := items.([]any)
		if !ok {
			return nil, changes, fmt.Errorf("field %q must be an array", spec.field)
		}
		for i, rawItem := range list {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return nil, changes, fmt.Errorf("%s[%d] must be an object", spec.field, i)
			}
			n, err := normalizeWikiItem(item, spec.kind, nil)
			changes += n
			if err != nil {
				return nil, changes, fmt.Errorf("%s[%d]: %w", spec.field, i, err)
			}
		}
	}
	return root, changes, nil
}

func normalizeWikiCitation(value any, req port.Request) (any, int, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("citation result must be an object")
	}
	registry, err := newCandidateRegistry(req.Candidates)
	if err != nil {
		return nil, 0, err
	}
	handles := make(map[string]struct{}, len(req.Handles))
	for _, handle := range req.Handles {
		handle = strings.TrimSpace(handle)
		if handle != "" {
			handles[handle] = struct{}{}
		}
	}

	rawCitations, exists := root["citations"]
	if !exists {
		return nil, 0, fmt.Errorf("missing required field %q", "citations")
	}
	citations, ok := rawCitations.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("field %q must be an object", "citations")
	}

	changes := 0
	canonical := make(map[string]any, len(citations))
	for rawSlug, rawHandles := range citations {
		slug, changed, err := registry.resolve(rawSlug)
		if err != nil {
			return nil, changes, fmt.Errorf("citation slug %q: %w", rawSlug, err)
		}
		if changed {
			changes++
		}
		list, ok := rawHandles.([]any)
		if !ok {
			return nil, changes, fmt.Errorf("citation %q must be an array", rawSlug)
		}
		validated, n, err := normalizeKnownStrings(list, handles, true)
		changes += n
		if err != nil {
			return nil, changes, fmt.Errorf("citation %q: %w", rawSlug, err)
		}
		if existing, found := canonical[slug]; found {
			merged, n := mergeStringArrays(existing.([]any), validated)
			canonical[slug] = merged
			changes += n + 1
		} else {
			canonical[slug] = validated
		}
	}
	root["citations"] = canonical

	if rawNewSlugs, exists := root["new_slugs"]; exists {
		newSlugs, ok := rawNewSlugs.([]any)
		if !ok {
			return nil, changes, fmt.Errorf("field %q must be an array", "new_slugs")
		}
		for i, rawItem := range newSlugs {
			item, ok := rawItem.(map[string]any)
			if !ok {
				return nil, changes, fmt.Errorf("new_slugs[%d] must be an object", i)
			}
			kind, ok := item["type"].(string)
			kind = strings.ToLower(strings.TrimSpace(kind))
			if !ok || (kind != "entity" && kind != "concept") {
				return nil, changes, fmt.Errorf("new_slugs[%d].type must be entity or concept", i)
			}
			if item["type"] != kind {
				item["type"] = kind
				changes++
			}
			n, err := normalizeWikiItem(item, kind, handles)
			changes += n
			if err != nil {
				return nil, changes, fmt.Errorf("new_slugs[%d]: %w", i, err)
			}
		}
	} else {
		root["new_slugs"] = []any{}
		changes++
	}

	return root, changes, nil
}

func normalizeWikiDedup(value any) (any, int, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("deduplication result must be an object")
	}
	rawMerges, exists := root["merges"]
	if !exists {
		return nil, 0, fmt.Errorf("missing required field %q", "merges")
	}
	merges, ok := rawMerges.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("field %q must be an object", "merges")
	}
	changes := 0
	normalized := make(map[string]any, len(merges))
	for rawSource, rawTarget := range merges {
		source := strings.TrimSpace(rawSource)
		target, ok := rawTarget.(string)
		target = strings.TrimSpace(target)
		if source == "" || !ok || target == "" {
			return nil, changes, fmt.Errorf("merge entries require non-empty string source and target")
		}
		if source != rawSource || target != rawTarget {
			changes++
		}
		normalized[source] = target
	}
	root["merges"] = normalized
	return root, changes, nil
}

func normalizeWikiTaxonomy(value any, req port.Request) (any, int, error) {
	root, ok := value.(map[string]any)
	if !ok {
		return nil, 0, fmt.Errorf("taxonomy result must be an object")
	}
	rawAssignments, exists := root["assignments"]
	if !exists {
		return nil, 0, fmt.Errorf("missing required field %q", "assignments")
	}
	assignments, ok := rawAssignments.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("field %q must be an array", "assignments")
	}
	registry, err := newCandidateRegistry(req.Candidates)
	if err != nil {
		return nil, 0, err
	}
	changes := 0
	for i, rawAssignment := range assignments {
		assignment, ok := rawAssignment.(map[string]any)
		if !ok {
			return nil, changes, fmt.Errorf("assignments[%d] must be an object", i)
		}
		rawSlug, ok := assignment["slug"].(string)
		if !ok {
			return nil, changes, fmt.Errorf("assignments[%d].slug must be a string", i)
		}
		slug, changed, err := registry.resolve(rawSlug)
		if err != nil {
			return nil, changes, fmt.Errorf("assignments[%d].slug: %w", i, err)
		}
		if changed {
			assignment["slug"] = slug
			changes++
		}
		path, ok := assignment["path"].([]any)
		if !ok {
			return nil, changes, fmt.Errorf("assignments[%d].path must be an array", i)
		}
		normalizedPath, n, err := normalizeKnownStrings(path, nil, false)
		changes += n
		if err != nil {
			return nil, changes, fmt.Errorf("assignments[%d].path: %w", i, err)
		}
		assignment["path"] = normalizedPath
	}
	return root, changes, nil
}

func normalizeWikiItem(item map[string]any, kind string, knownHandles map[string]struct{}) (int, error) {
	changes := 0
	name, ok := item["name"].(string)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return changes, fmt.Errorf("name must be a non-empty string")
	}
	if item["name"] != name {
		item["name"] = name
		changes++
	}
	rawSlug, ok := item["slug"].(string)
	if !ok {
		return changes, fmt.Errorf("slug must be a string")
	}
	slug, changed, err := canonicalWikiSlug(rawSlug, kind)
	if err != nil {
		return changes, err
	}
	if changed {
		item["slug"] = slug
		changes++
	}
	for _, field := range []string{"description", "details"} {
		if raw, exists := item[field]; exists {
			value, ok := raw.(string)
			if !ok {
				return changes, fmt.Errorf("%s must be a string", field)
			}
			trimmed := strings.TrimSpace(value)
			if trimmed != value {
				item[field] = trimmed
				changes++
			}
		}
	}
	if rawAliases, exists := item["aliases"]; exists {
		aliases, ok := rawAliases.([]any)
		if !ok {
			return changes, fmt.Errorf("aliases must be an array")
		}
		normalized, n, err := normalizeKnownStrings(aliases, nil, false)
		changes += n
		if err != nil {
			return changes, fmt.Errorf("aliases: %w", err)
		}
		item["aliases"] = normalized
	}
	if rawChunks, exists := item["source_chunks"]; exists {
		chunks, ok := rawChunks.([]any)
		if !ok {
			return changes, fmt.Errorf("source_chunks must be an array")
		}
		normalized, n, err := normalizeKnownStrings(chunks, knownHandles, knownHandles != nil)
		changes += n
		if err != nil {
			return changes, fmt.Errorf("source_chunks: %w", err)
		}
		item["source_chunks"] = normalized
	}
	return changes, nil
}

func canonicalWikiSlug(raw, kind string) (string, bool, error) {
	slug := strings.TrimSpace(raw)
	if slug == "" {
		return "", false, fmt.Errorf("slug must be non-empty")
	}
	prefix := kind + "/"
	if strings.HasPrefix(slug, prefix) {
		if strings.TrimPrefix(slug, prefix) == "" {
			return "", false, fmt.Errorf("slug suffix must be non-empty")
		}
		return slug, slug != raw, nil
	}
	if strings.Contains(slug, "/") {
		return "", false, fmt.Errorf("slug %q conflicts with expected %s namespace", slug, kind)
	}
	return prefix + slug, true, nil
}

type candidateRegistry struct {
	exact  map[string]struct{}
	byBare map[string][]string
}

func newCandidateRegistry(candidates []port.Candidate) (candidateRegistry, error) {
	registry := candidateRegistry{
		exact:  make(map[string]struct{}, len(candidates)),
		byBare: make(map[string][]string, len(candidates)),
	}
	for _, candidate := range candidates {
		kind := strings.ToLower(strings.TrimSpace(candidate.Kind))
		if kind != "entity" && kind != "concept" {
			return registry, fmt.Errorf("candidate %q has invalid kind %q", candidate.Slug, candidate.Kind)
		}
		slug, _, err := canonicalWikiSlug(candidate.Slug, kind)
		if err != nil {
			return registry, fmt.Errorf("candidate %q: %w", candidate.Slug, err)
		}
		if _, exists := registry.exact[slug]; exists {
			continue
		}
		registry.exact[slug] = struct{}{}
		bare := strings.TrimPrefix(slug, kind+"/")
		registry.byBare[bare] = append(registry.byBare[bare], slug)
	}
	return registry, nil
}

func (r candidateRegistry) resolve(raw string) (string, bool, error) {
	slug := strings.TrimSpace(raw)
	if slug == "" {
		return "", false, fmt.Errorf("slug must be non-empty")
	}
	if _, ok := r.exact[slug]; ok {
		return slug, slug != raw, nil
	}
	if strings.Contains(slug, "/") {
		return "", false, fmt.Errorf("unknown candidate slug %q", slug)
	}
	matches := r.byBare[slug]
	if len(matches) == 1 {
		return matches[0], true, nil
	}
	if len(matches) == 0 {
		return "", false, fmt.Errorf("unknown candidate slug %q", slug)
	}
	return "", false, fmt.Errorf("ambiguous bare slug %q", slug)
}

func normalizeKnownStrings(values []any, known map[string]struct{}, requireKnown bool) ([]any, int, error) {
	out := make([]any, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	changes := 0
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			return nil, changes, fmt.Errorf("all values must be strings")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, changes, fmt.Errorf("values must be non-empty")
		}
		if requireKnown {
			if _, ok := known[trimmed]; !ok {
				return nil, changes, fmt.Errorf("unknown handle %q", trimmed)
			}
		}
		if _, duplicate := seen[trimmed]; duplicate {
			changes++
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if trimmed != value {
			changes++
		}
	}
	return out, changes, nil
}

func mergeStringArrays(left, right []any) ([]any, int) {
	out := append([]any(nil), left...)
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, value := range left {
		if text, ok := value.(string); ok {
			seen[text] = struct{}{}
		}
	}
	changes := 0
	for _, value := range right {
		text, _ := value.(string)
		if _, exists := seen[text]; exists {
			changes++
			continue
		}
		seen[text] = struct{}{}
		out = append(out, value)
	}
	return out, changes
}
