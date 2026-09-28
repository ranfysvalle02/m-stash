package main

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCreatorProfileInputNormalizesAndValidates(t *testing.T) {
	input := creatorProfileInput{
		Handle:      " Ada-Lovelace ",
		DisplayName: " Ada Lovelace ",
		AvatarURL:   "https://example.com/ada.png",
		Links:       []profileLink{{Label: " Website ", URL: "https://example.com"}},
	}
	if err := input.normalizeAndValidate(); err != nil {
		t.Fatalf("normalizeAndValidate() error = %v", err)
	}
	if input.Handle != "ada-lovelace" || input.DisplayName != "Ada Lovelace" || input.Links[0].Label != "Website" {
		t.Fatalf("normalized profile input = %#v", input)
	}

	input.AvatarURL = "javascript:alert(1)"
	if err := input.normalizeAndValidate(); err == nil {
		t.Fatal("unsafe avatar URL was accepted")
	}
}

func TestCreatorStashInputNormalizesTags(t *testing.T) {
	input := creatorStashInput{Title: " Shipping notes ", Tags: []string{" Go ", "go", "Release"}}
	if err := input.normalizeAndValidate(); err != nil {
		t.Fatalf("normalizeAndValidate() error = %v", err)
	}
	if input.Title != "Shipping notes" || len(input.Tags) != 2 || input.Tags[0] != "go" || input.Tags[1] != "release" {
		t.Fatalf("normalized stash input = %#v", input)
	}
}

func TestCreatorStashBSONRetainsAuthoredFields(t *testing.T) {
	createdAt := time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)
	stash := creatorStash{
		ID:        bson.NewObjectID(),
		OwnerID:   bson.NewObjectID(),
		Title:     "Shipping notes",
		Summary:   "A concise update.",
		Content:   "# Details",
		Tags:      []string{"release"},
		IsPublic:  true,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	encoded, err := bson.Marshal(stash)
	if err != nil {
		t.Fatalf("marshal creator stash: %v", err)
	}
	var decoded bson.M
	if err := bson.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal creator stash: %v", err)
	}
	if decoded["title"] != stash.Title || decoded["isPublic"] != true || decoded["content"] != stash.Content {
		t.Fatalf("creator stash BSON omitted authored fields: %#v", decoded)
	}
}
