package main

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCompileResourceFilterAllowsAllowlistedPredicates(t *testing.T) {
	compiled, err := compileResourceFilter(`{"data.score":{"$gte":100},"visibility":"public"}`)
	if err != nil {
		t.Fatalf("valid filter rejected: %v", err)
	}
	score, ok := compiled["data.score"].(bson.M)
	if !ok {
		t.Fatalf("data.score predicate = %#v", compiled["data.score"])
	}
	if value, ok := score["$gte"].(int64); !ok || value != 100 {
		t.Fatalf("$gte operand = %#v", score["$gte"])
	}
	if compiled["visibility"] != "public" {
		t.Fatalf("visibility equality = %#v", compiled["visibility"])
	}
}

func TestCompileResourceFilterCompilesLogicalBranches(t *testing.T) {
	compiled, err := compileResourceFilter(`{"$or":[{"data.rank":{"$in":[1,2,3]}},{"data.featured":true}]}`)
	if err != nil {
		t.Fatalf("logical filter rejected: %v", err)
	}
	branches, ok := compiled["$or"].(bson.A)
	if !ok || len(branches) != 2 {
		t.Fatalf("$or branches = %#v", compiled["$or"])
	}
}

func TestCompileResourceFilterParsesTimestamps(t *testing.T) {
	compiled, err := compileResourceFilter(`{"createdAt":{"$gt":"2026-01-01T00:00:00Z"}}`)
	if err != nil {
		t.Fatalf("timestamp filter rejected: %v", err)
	}
	predicate := compiled["createdAt"].(bson.M)
	if _, ok := predicate["$gt"]; !ok {
		t.Fatalf("createdAt predicate = %#v", predicate)
	}
}

func TestCompileResourceFilterRejectsUnsafeInput(t *testing.T) {
	cases := map[string]string{
		"$where injection":      `{"$where":"sleep(1000)"}`,
		"unknown operator":      `{"data.score":{"$regex":"^a"}}`,
		"non-allowlisted field": `{"namespaceId":"abc"}`,
		"raw subdocument value": `{"data.meta":{"nested":1}}`,
		"operator field inject": `{"data.$ne":1}`,
		"bad timestamp":         `{"updatedAt":{"$gt":"not-a-date"}}`,
		"empty logical array":   `{"$and":[]}`,
		"non-object body":       `[1,2,3]`,
	}
	for name, raw := range cases {
		if _, err := compileResourceFilter(raw); err == nil {
			t.Errorf("unsafe filter %q was accepted", name)
		}
	}
}
