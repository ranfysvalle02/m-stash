package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const maxFilterDepth = 8

const maxSearchQueryLength = 200

// resourceSearchResult is a list preview plus the full-text relevance score.
type resourceSearchResult struct {
	resourcePreview `bson:",inline"`
	Score           float64 `bson:"score" json:"score"`
}

// allowedFilterComparisons are the only value-level operators a caller may use.
var allowedFilterComparisons = map[string]struct{}{
	"$eq": {}, "$ne": {}, "$gt": {}, "$gte": {}, "$lt": {}, "$lte": {}, "$in": {}, "$nin": {}, "$not": {},
}

// allowedFilterSystemFields are the indexed non-data fields a caller may filter on.
var allowedFilterSystemFields = map[string]struct{}{
	"slug": {}, "visibility": {}, "createdAt": {}, "updatedAt": {},
}

// compileResourceFilter parses a caller-supplied JSON filter into a BSON document restricted
// to an allowlisted operator/field set. It never produces an operator that can read other
// collections or run server-side code, and scalar-only values block raw sub-document injection.
func compileResourceFilter(raw string) (bson.M, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(raw)))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("filter must be a JSON object")
	}
	if decoder.More() {
		return nil, errors.New("filter must be a single JSON object")
	}
	return compileFilterDocument(document, 0)
}

func compileFilterDocument(document map[string]any, depth int) (bson.M, error) {
	if depth > maxFilterDepth {
		return nil, errors.New("filter is nested too deeply")
	}
	if len(document) == 0 {
		return bson.M{}, nil
	}
	compiled := bson.M{}
	for key, value := range document {
		if key == "$and" || key == "$or" {
			branch, err := compileFilterBranches(value, depth)
			if err != nil {
				return nil, err
			}
			compiled[key] = branch
			continue
		}
		if len(key) > 0 && key[0] == '$' {
			return nil, errors.New("unsupported filter operator: " + key)
		}
		if err := validateFilterField(key); err != nil {
			return nil, err
		}
		predicate, err := compileFieldPredicate(key, value, depth)
		if err != nil {
			return nil, err
		}
		compiled[key] = predicate
	}
	return compiled, nil
}

func compileFilterBranches(value any, depth int) (bson.A, error) {
	branches, ok := value.([]any)
	if !ok || len(branches) == 0 {
		return nil, errors.New("$and/$or require a non-empty array")
	}
	compiled := make(bson.A, 0, len(branches))
	for _, branch := range branches {
		document, ok := branch.(map[string]any)
		if !ok {
			return nil, errors.New("$and/$or entries must be objects")
		}
		sub, err := compileFilterDocument(document, depth+1)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, sub)
	}
	return compiled, nil
}

func compileFieldPredicate(field string, value any, depth int) (any, error) {
	if depth > maxFilterDepth {
		return nil, errors.New("filter is nested too deeply")
	}
	expression, ok := value.(map[string]any)
	if !ok {
		return convertFilterScalar(field, value)
	}
	compiled := bson.M{}
	for operator, operand := range expression {
		if _, allowed := allowedFilterComparisons[operator]; !allowed {
			return nil, errors.New("unsupported filter operator: " + operator)
		}
		switch operator {
		case "$in", "$nin":
			values, ok := operand.([]any)
			if !ok {
				return nil, errors.New(operator + " requires an array")
			}
			converted := make(bson.A, 0, len(values))
			for _, element := range values {
				scalar, err := convertFilterScalar(field, element)
				if err != nil {
					return nil, err
				}
				converted = append(converted, scalar)
			}
			compiled[operator] = converted
		case "$not":
			inner, ok := operand.(map[string]any)
			if !ok {
				return nil, errors.New("$not requires an operator object")
			}
			nested, err := compileFieldPredicate(field, inner, depth+1)
			if err != nil {
				return nil, err
			}
			if _, ok := nested.(bson.M); !ok {
				return nil, errors.New("$not requires an operator object")
			}
			compiled[operator] = nested
		default:
			scalar, err := convertFilterScalar(field, operand)
			if err != nil {
				return nil, err
			}
			compiled[operator] = scalar
		}
	}
	return compiled, nil
}

func validateFilterField(field string) error {
	if _, ok := allowedFilterSystemFields[field]; ok {
		return nil
	}
	if err := validateIncrementFieldPath(field); err != nil {
		return errors.New("field is not filterable: " + field)
	}
	return nil
}

func convertFilterScalar(field string, value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer, nil
		}
		number, err := typed.Float64()
		if err != nil {
			return nil, errors.New("filter value is not a valid number")
		}
		return number, nil
	case string:
		if field == "createdAt" || field == "updatedAt" {
			parsed, err := time.Parse(time.RFC3339, typed)
			if err != nil {
				return nil, errors.New(field + " filter value must be an RFC3339 timestamp")
			}
			return parsed, nil
		}
		return typed, nil
	case bool:
		return typed, nil
	default:
		return nil, errors.New("filter values must be strings, numbers, or booleans")
	}
}

// parseResourceFilterParam compiles the optional ?filter= JSON object, returning nil when absent.
func parseResourceFilterParam(r *http.Request) (bson.M, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("filter"))
	if raw == "" {
		return nil, nil
	}
	return compileResourceFilter(raw)
}

// parseResourceSearchQuery returns the trimmed, length-capped ?q= search term, or "" when absent.
func parseResourceSearchQuery(r *http.Request) (string, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		return "", nil
	}
	if len(query) > maxSearchQueryLength {
		return "", errors.New("q must be 200 characters or fewer")
	}
	return query, nil
}

func resourceCountRequested(r *http.Request) bool {
	switch strings.ToLower(r.URL.Query().Get("count")) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// mergeResourceFilter ANDs an extra predicate onto the base scope so a caller filter can only narrow results.
func mergeResourceFilter(base, extra bson.M) bson.M {
	if len(extra) == 0 {
		return base
	}
	return bson.M{"$and": bson.A{base, extra}}
}

func (app *application) writeResourceSearch(w http.ResponseWriter, r *http.Request, filter bson.M, limit int) {
	findOptions := options.Find().
		SetSort(bson.D{{Key: "score", Value: bson.M{"$meta": "textScore"}}}).
		SetLimit(int64(limit)).
		SetProjection(bson.M{
			"_id": 1, "type": 1, "slug": 1, "title": 1, "summary": 1, "visibility": 1,
			"createdAt": 1, "updatedAt": 1, "score": bson.M{"$meta": "textScore"},
		})
	mongoCursor, err := app.database.Collection(resourceCollectionName).Find(r.Context(), filter, findOptions)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not search resources")
		return
	}
	defer mongoCursor.Close(r.Context())
	results := make([]resourceSearchResult, 0)
	if err := mongoCursor.All(r.Context(), &results); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not decode search results")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": results, "page": map[string]any{"limit": limit}})
}
