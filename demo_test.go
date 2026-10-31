package main

import "testing"

func TestDemoRecordsAreValidAndUnique(t *testing.T) {
	records := demoRecords()
	if len(records) == 0 {
		t.Fatal("expected at least one demo record")
	}
	seen := make(map[string]bool)
	for _, record := range records {
		if err := validateResourceType(record.Type); err != nil {
			t.Fatalf("demo record type %q invalid: %v", record.Type, err)
		}
		if err := validateResourceSlug(record.Slug); err != nil {
			t.Fatalf("demo record slug %q invalid: %v", record.Slug, err)
		}
		key := record.Type + "/" + record.Slug
		if seen[key] {
			t.Fatalf("duplicate demo record %q", key)
		}
		seen[key] = true
		if record.Title == "" || record.Summary == "" || record.Content == "" {
			t.Fatalf("demo record %q missing title/summary/content", key)
		}
		if len(record.Data) == 0 {
			t.Fatalf("demo record %q missing data payload", key)
		}
	}
}
