package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func isAllowedClientQueryOperator(operator string) bool {
	switch operator {
	case "$and", "$or", "$eq", "$ne", "$in", "$nin", "$gt", "$gte", "$lt", "$lte", "$exists", "$all":
		return true
	default:
		return false
	}
}

func isIDField(key string) bool {
	if key == "" || strings.HasPrefix(key, "$") {
		return false
	}
	return key == "_id" || strings.HasSuffix(key, "Id") || strings.HasSuffix(key, "_id") || strings.HasSuffix(key, "ID")
}

func parseObjectID(value string, keyContext string) any {
	if isIDField(keyContext) && len(value) == 24 {
		if objectID, err := bson.ObjectIDFromHex(value); err == nil {
			return objectID
		}
	}
	return value
}

func interpolateAndNormalize(node any, claims *Claims, keyContext string) any {
	switch value := node.(type) {
	case string:
		var interpolated string
		switch value {
		case "$auth.uid":
			interpolated = claims.UID
		case "$auth.email":
			interpolated = claims.Email
		case "$auth.role":
			interpolated = claims.Role
		default:
			interpolated = strings.ReplaceAll(value, "$auth.uid", claims.UID)
			interpolated = strings.ReplaceAll(interpolated, "$auth.role", claims.Role)
			interpolated = strings.ReplaceAll(interpolated, "$auth.email", claims.Email)
		}
		return parseObjectID(interpolated, keyContext)

	case map[string]any:
		result := make(map[string]any)
		for key, child := range value {
			nextContext := key
			if strings.HasPrefix(key, "$") {
				nextContext = keyContext
			}
			result[key] = interpolateAndNormalize(child, claims, nextContext)
		}
		return result

	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = interpolateAndNormalize(child, claims, keyContext)
		}
		return result

	default:
		return value
	}
}

func normalizeClientQuery(node any, keyContext string) any {
	switch value := node.(type) {
	case string:
		return parseObjectID(value, keyContext)
	case map[string]any:
		result := make(map[string]any)
		for key, child := range value {
			nextContext := key
			if strings.HasPrefix(key, "$") {
				nextContext = keyContext
			}
			result[key] = normalizeClientQuery(child, nextContext)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = normalizeClientQuery(child, keyContext)
		}
		return result
	default:
		return value
	}
}

func validateClientQuery(node any, depth int) error {
	if depth > 16 {
		return errors.New("query nesting exceeds the maximum depth")
	}

	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.ContainsRune(key, '\x00') {
				return errors.New("query contains an invalid field name")
			}
			if strings.HasPrefix(key, "$") && !isAllowedClientQueryOperator(key) {
				return fmt.Errorf("query operator %q is not allowed", key)
			}
			if key == "$and" || key == "$or" {
				if _, ok := child.([]any); !ok {
					return fmt.Errorf("query operator %q requires an array", key)
				}
			}
			if err := validateClientQuery(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := validateClientQuery(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateQueryLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultQueryPageSize, nil
	}
	if limit < 1 || limit > maxQueryPageSize {
		return 0, fmt.Errorf("limit must be an integer between 1 and %d", maxQueryPageSize)
	}
	return limit, nil
}

func valuesEqual(first, second any) bool {
	if reflect.DeepEqual(first, second) {
		return true
	}

	firstString := fmt.Sprintf("%v", first)
	secondString := fmt.Sprintf("%v", second)
	if firstString == secondString {
		return true
	}
	if objectID, ok := first.(bson.ObjectID); ok {
		return objectID.Hex() == secondString
	}
	if objectID, ok := second.(bson.ObjectID); ok {
		return objectID.Hex() == firstString
	}
	return false
}

func validateFieldWriteMask(payload map[string]any, rule CollectionRule) error {
	if payload == nil {
		return nil
	}

	for _, restricted := range rule.RestrictedWriteFields {
		if _, exists := payload[restricted]; exists {
			return fmt.Errorf("field '%s' is restricted and cannot be written by clients", restricted)
		}
	}

	if len(rule.AllowedWriteFields) > 0 {
		allowedFields := make(map[string]bool)
		for _, field := range rule.AllowedWriteFields {
			allowedFields[field] = true
		}
		for field := range payload {
			if !allowedFields[field] {
				return fmt.Errorf("field '%s' is not in the allowed write schema", field)
			}
		}
	}
	return nil
}

func applySecurityFilter(clientQuery map[string]any, rawRule any, claims *Claims) (bson.M, error) {
	if rawRule == nil {
		return nil, errors.New("permission denied: no read policy configured")
	}
	if err := validateClientQuery(clientQuery, 0); err != nil {
		return nil, err
	}

	normalizedClientQuery, _ := normalizeClientQuery(clientQuery, "").(map[string]any)
	if allowed, ok := rawRule.(bool); ok {
		if !allowed {
			return nil, errors.New("permission denied: read operation prohibited")
		}
		if normalizedClientQuery == nil {
			return bson.M{}, nil
		}
		return bson.M(normalizedClientQuery), nil
	}

	interpolated := interpolateAndNormalize(rawRule, claims, "")
	ruleMap, ok := interpolated.(map[string]any)
	if !ok {
		return nil, errors.New("invalid security rule structure")
	}
	if len(normalizedClientQuery) == 0 {
		return bson.M(ruleMap), nil
	}
	return bson.M{"$and": []any{normalizedClientQuery, ruleMap}}, nil
}

func authorizeAndHydrateInsert(payload map[string]any, rule CollectionRule, claims *Claims) (map[string]any, error) {
	if rule.Write == nil {
		return nil, errors.New("write access denied: no write policy configured")
	}
	if payload == nil {
		payload = make(map[string]any)
	}
	if err := validateFieldWriteMask(payload, rule); err != nil {
		return nil, err
	}
	if allowed, ok := rule.Write.(bool); ok {
		if !allowed {
			return nil, errors.New("write access denied by policy")
		}
		return payload, nil
	}

	ruleMap, ok := rule.Write.(map[string]any)
	if !ok {
		return nil, errors.New("invalid write rule configuration")
	}
	interpolatedRule, _ := interpolateAndNormalize(ruleMap, claims, "").(map[string]any)
	for key, expectedValue := range interpolatedRule {
		if strings.HasPrefix(key, "$") {
			return nil, fmt.Errorf("complex query operators (%s) are not supported in insert write rules", key)
		}
		existingValue, exists := payload[key]
		if !exists {
			payload[key] = expectedValue
		} else if !valuesEqual(existingValue, expectedValue) {
			return nil, fmt.Errorf("payload field '%s' violates security rule policy", key)
		}
	}
	return payload, nil
}

func sanitizeUpdatePayload(payload map[string]any, rule CollectionRule, claims *Claims) (map[string]any, error) {
	if payload == nil {
		return nil, errors.New("update payload cannot be empty")
	}
	delete(payload, "_id")
	if err := validateFieldWriteMask(payload, rule); err != nil {
		return nil, err
	}
	if rule.Write == nil {
		return nil, errors.New("write access denied: no write policy configured")
	}
	if allowed, ok := rule.Write.(bool); ok {
		if !allowed {
			return nil, errors.New("write access denied by policy")
		}
		return payload, nil
	}

	ruleMap, ok := rule.Write.(map[string]any)
	if !ok {
		return nil, errors.New("invalid write rule configuration")
	}
	interpolatedRule, _ := interpolateAndNormalize(ruleMap, claims, "").(map[string]any)
	for key, expectedValue := range interpolatedRule {
		if strings.HasPrefix(key, "$") {
			continue
		}
		if existingValue, exists := payload[key]; exists && !valuesEqual(existingValue, expectedValue) {
			return nil, fmt.Errorf("cannot modify restricted field '%s' to a value violating policy", key)
		}
	}
	return payload, nil
}

func validateProfilePayload(payload map[string]any) error {
	handle, exists := payload["handle"]
	if !exists {
		return nil
	}
	handleString, ok := handle.(string)
	if !ok || !isProfileHandle(handleString) {
		return errors.New("profile handle must contain 3-32 lowercase letters, numbers, hyphens, or underscores")
	}
	return nil
}

func validateStashPayload(payload map[string]any) error {
	title, exists := payload["title"]
	if !exists {
		return errors.New("stash title is required")
	}
	titleString, ok := title.(string)
	if !ok || strings.TrimSpace(titleString) == "" || len(titleString) > 200 {
		return errors.New("stash title must be a non-empty string up to 200 characters")
	}
	return nil
}
