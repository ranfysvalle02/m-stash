package main

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestInferSpecFromResourcesReadsShapesNotValues(t *testing.T) {
	shared := bson.NewObjectID()
	personal := bson.NewObjectID()
	resources := []Resource{
		{Type: "menu", NamespaceID: shared, Visibility: publicVisibility, Data: bson.M{"dish": "Ramen", "price": 12.5, "vegan": true}},
		{Type: "menu", NamespaceID: shared, Visibility: publicVisibility, Data: bson.M{"dish": "Gyoza", "price": int32(6)}},
		{Type: "orders", NamespaceID: personal, Visibility: privateVisibility, Data: bson.M{"placed_on": "2026-05-01", "items": bson.A{"ramen"}, "camelCase": "x", "meta": bson.M{"a": 1}}},
		{Type: "profile", NamespaceID: personal, Visibility: publicVisibility, Data: bson.M{"bio": "hi"}},
	}
	spec := validateVibeSpec(inferSpecFromResources(resources, shared))
	if len(spec.Sections) != 3 || spec.Sections[0].Type != "menu" || spec.Sections[0].Audience != vibeAudienceOwner || spec.Sections[0].View != "grid" {
		t.Fatalf("owner section should lead as a grid: %+v", spec.Sections)
	}
	kinds := map[string]string{}
	for _, section := range spec.Sections {
		if len(section.Samples) != 0 {
			t.Fatalf("an inferred spec must never carry record values: %+v", section)
		}
		for _, field := range section.Fields {
			kinds[section.Type+"."+field.Key] = field.Kind
		}
	}
	want := map[string]string{"menu.dish": "text", "menu.price": "price", "menu.vegan": "toggle", "orders.placed_on": "date", "orders.items": "tag", "profile.bio": "text"}
	for key, kind := range want {
		if kinds[key] != kind {
			t.Errorf("%s kind = %q, want %q (all: %v)", key, kinds[key], kind, kinds)
		}
	}
	// Keys the renderer could not read verbatim, and nested objects, are left out.
	if _, ok := kinds["orders.camelCase"]; ok {
		t.Error("non-snake_case keys must be skipped")
	}
	if _, ok := kinds["orders.meta"]; ok {
		t.Error("nested objects must be skipped")
	}
	byType := map[string]vibeSection{}
	for _, section := range spec.Sections {
		byType[section.Type] = section
	}
	if byType["orders"].Audience != vibeAudienceMember || byType["orders"].View != "timeline" {
		t.Errorf("private dated records should be a member timeline: %+v", byType["orders"])
	}
	if byType["profile"].Audience != vibeAudienceMemberPublic {
		t.Errorf("mostly-public personal records should be member-public: %+v", byType["profile"])
	}
}

func TestInferSpecFromNoResourcesIsStillRenderable(t *testing.T) {
	spec := validateVibeSpec(inferSpecFromResources(nil, bson.NilObjectID))
	if len(spec.Sections) != 1 || len(spec.Sections[0].Fields) == 0 {
		t.Fatalf("an empty app should get a usable starter section: %+v", spec)
	}
}
