package main

import (
	"testing"
	"time"
)

func TestMatchesEventType(t *testing.T) {
	cases := []struct {
		patterns  []string
		eventType string
		want      bool
	}{
		{[]string{"*"}, "m-stash.resources.increment.v1", true},
		{[]string{"m-stash.resources.increment.v1"}, "m-stash.resources.increment.v1", true},
		{[]string{"m-stash.resources"}, "m-stash.resources.increment.v1", true},
		{[]string{"m-stash.resources."}, "m-stash.resources.updateOne.v1", true},
		{[]string{"m-stash.namespaces"}, "m-stash.resources.increment.v1", false},
		{[]string{"m-stash.resource"}, "m-stash.resources.increment.v1", false},
	}
	for _, testCase := range cases {
		if got := matchesEventType(testCase.patterns, testCase.eventType); got != testCase.want {
			t.Errorf("matchesEventType(%v, %q) = %t", testCase.patterns, testCase.eventType, got)
		}
	}
}

func TestSignWebhookPayloadIsStableAndSecretDependent(t *testing.T) {
	payload := []byte(`{"id":"abc"}`)
	first := signWebhookPayload("secret-one", payload)
	if first != signWebhookPayload("secret-one", payload) {
		t.Fatal("signature is not deterministic")
	}
	if first == signWebhookPayload("secret-two", payload) {
		t.Fatal("signature did not depend on the secret")
	}
	if first[:7] != "sha256=" {
		t.Fatalf("signature is missing algorithm prefix: %q", first)
	}
}

func TestWebhookBackoffGrowsAndCaps(t *testing.T) {
	base := 10 * time.Second
	maximum := time.Hour
	if got := webhookBackoff(1, base, maximum); got != base {
		t.Fatalf("first attempt backoff = %s", got)
	}
	if got := webhookBackoff(2, base, maximum); got != 20*time.Second {
		t.Fatalf("second attempt backoff = %s", got)
	}
	if got := webhookBackoff(50, base, maximum); got != maximum {
		t.Fatalf("large attempt backoff = %s", got)
	}
}

func TestWebhookInputNormalizeAndValidate(t *testing.T) {
	valid := webhookInput{URL: " https://example.com/hook ", EventTypes: []string{" m-stash.resources ", "*"}}
	if err := valid.normalizeAndValidate(); err != nil {
		t.Fatalf("valid webhook rejected: %v", err)
	}
	if valid.URL != "https://example.com/hook" || valid.EventTypes[0] != "m-stash.resources" {
		t.Fatalf("webhook input not normalized: %#v", valid)
	}

	for name, input := range map[string]webhookInput{
		"missing url":    {URL: "", EventTypes: []string{"*"}},
		"relative url":   {URL: "/hook", EventTypes: []string{"*"}},
		"no event types": {URL: "https://example.com", EventTypes: nil},
		"bad event type": {URL: "https://example.com", EventTypes: []string{"bad type!"}},
	} {
		if err := input.normalizeAndValidate(); err == nil {
			t.Errorf("invalid webhook %q was accepted", name)
		}
	}
}
