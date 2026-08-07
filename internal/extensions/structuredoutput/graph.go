package structuredoutput

import (
	"fmt"
	"strings"
)

var (
	graphSourceAliases    = []string{"entity1", "subject", "head_entity", "head", "source", "from"}
	graphTargetAliases    = []string{"entity2", "object", "tail_entity", "tail", "target", "to"}
	graphRelationAliases  = []string{"relation", "predicate", "relation_type", "relationship_type", "type", "rel"}
	graphNodeAliases      = []string{"entity", "node", "name"}
	graphAttributeAliases = []string{
		"entity_attributes", "attributes", "attrs", "properties", "props",
	}
)

func normalizeGraphDocument(value any) (any, int, error) {
	if root, ok := value.(map[string]any); ok {
		if len(root) == 0 {
			return []any{}, 1, nil
		}
		if _, hasNodes := root["nodes"]; hasNodes {
			return normalizeCanonicalGraph(root)
		}
		if _, hasRelations := root["relations"]; hasRelations {
			return normalizeCanonicalGraph(root)
		}
		return normalizeLegacyGraphGroups([]any{root}, 1)
	}
	groups, ok := value.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("graph output must be an object or array")
	}
	if len(groups) == 0 {
		return groups, 0, nil
	}
	return normalizeLegacyGraphGroups(groups, 0)
}

func normalizeCanonicalGraph(root map[string]any) (any, int, error) {
	changes := 1 // canonical object is converted to the legacy array consumed by Formater.ParseGraph.
	out := make([]any, 0)
	if rawNodes, exists := root["nodes"]; exists {
		nodes, ok := rawNodes.([]any)
		if !ok {
			return nil, changes, fmt.Errorf("nodes must be an array")
		}
		for i, rawNode := range nodes {
			node, ok := rawNode.(map[string]any)
			if !ok {
				return nil, changes, fmt.Errorf("nodes[%d] must be an object", i)
			}
			nameValue, key, found := firstValue(node, []string{"name", "entity", "node"})
			if !found {
				return nil, changes, fmt.Errorf("nodes[%d] has no name", i)
			}
			name, err := graphEndpointName(nameValue)
			if err != nil {
				return nil, changes, fmt.Errorf("nodes[%d].%s: %w", i, key, err)
			}
			group := map[string]any{"entity": name}
			if rawAttrs, _, found := firstValue(node, graphAttributeAliases); found {
				attrs, err := graphAttributes(rawAttrs)
				if err != nil {
					return nil, changes, fmt.Errorf("nodes[%d].attributes: %w", i, err)
				}
				group["entity_attributes"] = attrs
			}
			out = append(out, group)
		}
	}
	if rawRelations, exists := root["relations"]; exists {
		relations, ok := rawRelations.([]any)
		if !ok {
			return nil, changes, fmt.Errorf("relations must be an array")
		}
		for i, rawRelation := range relations {
			relation, ok := rawRelation.(map[string]any)
			if !ok {
				return nil, changes, fmt.Errorf("relations[%d] must be an object", i)
			}
			group, _, err := normalizeRelationGroup(relation)
			if err != nil {
				return nil, changes, fmt.Errorf("relations[%d]: %w", i, err)
			}
			out = append(out, group)
		}
	}
	return out, changes, nil
}

func normalizeLegacyGraphGroups(groups []any, initialChanges int) (any, int, error) {
	out := make([]any, 0, len(groups))
	changes := initialChanges
	for i, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return nil, changes, fmt.Errorf("graph group %d must be an object", i)
		}
		if len(group) == 0 {
			return nil, changes, fmt.Errorf("graph group %d is empty", i)
		}

		_, _, hasSource := firstValue(group, graphSourceAliases)
		_, _, hasTarget := firstValue(group, graphTargetAliases)
		if hasSource || hasTarget {
			normalized, n, err := normalizeRelationGroup(group)
			changes += n
			if err != nil {
				return nil, changes, fmt.Errorf("graph group %d: %w", i, err)
			}
			out = append(out, normalized)
			continue
		}

		nameValue, key, found := firstValue(group, graphNodeAliases)
		if !found {
			return nil, changes, fmt.Errorf("graph group %d has no recognized node or relation fields", i)
		}
		name, err := graphEndpointName(nameValue)
		if err != nil {
			return nil, changes, fmt.Errorf("graph group %d.%s: %w", i, key, err)
		}
		normalized := map[string]any{"entity": name}
		if rawAttrs, attrKey, found := firstValue(group, graphAttributeAliases); found {
			attrs, err := graphAttributes(rawAttrs)
			if err != nil {
				return nil, changes, fmt.Errorf("graph group %d.%s: %w", i, attrKey, err)
			}
			normalized["entity_attributes"] = attrs
			if attrKey != "entity_attributes" {
				changes++
			}
		}
		if key != "entity" || !sameStringValue(nameValue, name) {
			changes++
		}
		out = append(out, normalized)
	}
	return out, changes, nil
}

func normalizeRelationGroup(group map[string]any) (map[string]any, int, error) {
	sourceValue, sourceKey, hasSource := firstValue(group, graphSourceAliases)
	targetValue, targetKey, hasTarget := firstValue(group, graphTargetAliases)
	relationValue, relationKey, hasRelation := firstValue(group, graphRelationAliases)
	if !hasSource || !hasTarget || !hasRelation {
		return nil, 0, fmt.Errorf("relation requires source, target, and type")
	}
	source, err := graphEndpointName(sourceValue)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", sourceKey, err)
	}
	target, err := graphEndpointName(targetValue)
	if err != nil {
		return nil, 0, fmt.Errorf("%s: %w", targetKey, err)
	}
	relation, ok := relationValue.(string)
	relation = strings.TrimSpace(relation)
	if !ok || relation == "" {
		return nil, 0, fmt.Errorf("%s must be a non-empty string", relationKey)
	}
	changes := 0
	if sourceKey != "entity1" || targetKey != "entity2" || relationKey != "relation" {
		changes++
	}
	if !sameStringValue(sourceValue, source) || !sameStringValue(targetValue, target) || !sameStringValue(relationValue, relation) {
		changes++
	}
	return map[string]any{
		"entity1":  source,
		"entity2":  target,
		"relation": relation,
	}, changes, nil
}

func normalizeLegacyEntities(value any) (any, int, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("legacy entity result must be an array")
	}
	changes := 0
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, changes, fmt.Errorf("entity %d must be an object", i)
		}
		n, err := normalizeRequiredString(item, "title")
		changes += n
		if err != nil {
			return nil, changes, fmt.Errorf("entity %d: %w", i, err)
		}
		for _, field := range []string{"type", "description"} {
			if _, exists := item[field]; exists {
				n, err := normalizeRequiredString(item, field)
				changes += n
				if err != nil {
					return nil, changes, fmt.Errorf("entity %d: %w", i, err)
				}
			}
		}
	}
	return items, changes, nil
}

func normalizeLegacyRelations(value any) (any, int, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, 0, fmt.Errorf("legacy relationship result must be an array")
	}
	changes := 0
	for i, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, changes, fmt.Errorf("relationship %d must be an object", i)
		}
		for _, field := range []string{"source", "target"} {
			n, err := normalizeRequiredString(item, field)
			changes += n
			if err != nil {
				return nil, changes, fmt.Errorf("relationship %d: %w", i, err)
			}
		}
		if _, exists := item["description"]; exists {
			n, err := normalizeRequiredString(item, "description")
			changes += n
			if err != nil {
				return nil, changes, fmt.Errorf("relationship %d: %w", i, err)
			}
		}
	}
	return items, changes, nil
}

func firstValue(object map[string]any, aliases []string) (any, string, bool) {
	for _, alias := range aliases {
		if value, ok := object[alias]; ok && value != nil {
			return value, alias, true
		}
	}
	return nil, "", false
}

func graphEndpointName(value any) (string, error) {
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("endpoint must be non-empty")
		}
		return text, nil
	}
	if object, ok := value.(map[string]any); ok {
		for _, field := range []string{"text", "name", "id"} {
			if raw, found := object[field]; found {
				text, ok := raw.(string)
				text = strings.TrimSpace(text)
				if ok && text != "" {
					return text, nil
				}
			}
		}
	}
	return "", fmt.Errorf("endpoint must be a non-empty string or an object with text, name, or id")
}

func graphAttributes(value any) ([]any, error) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []any{}, nil
		}
		return []any{trimmed}, nil
	case []any:
		result, _, err := normalizeKnownStrings(typed, nil, false)
		return result, err
	default:
		return nil, fmt.Errorf("attributes must be a string or string array")
	}
}

func normalizeRequiredString(object map[string]any, field string) (int, error) {
	raw, ok := object[field]
	if !ok {
		return 0, fmt.Errorf("missing required field %q", field)
	}
	value, ok := raw.(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return 0, fmt.Errorf("%s must be a non-empty string", field)
	}
	if value != raw {
		object[field] = value
		return 1, nil
	}
	return 0, nil
}

func sameStringValue(raw any, normalized string) bool {
	value, ok := raw.(string)
	return ok && value == normalized
}
