package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
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

func TestIsBlockedWebhookAddr(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "0.0.0.0", "::",
		"10.0.0.5", "172.16.3.4", "192.168.1.1", "fd00::1",
		"169.254.169.254", "fe80::1",
		"100.64.0.1", "192.0.0.1", "192.0.2.5", "198.18.0.1",
		"198.51.100.3", "203.0.113.9", "240.0.0.1",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254",
		"2001:db8::1",
	}
	for _, raw := range blocked {
		if !isBlockedWebhookAddr(netip.MustParseAddr(raw)) {
			t.Errorf("isBlockedWebhookAddr(%s) = false, want true", raw)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:2800:220:1::1"}
	for _, raw := range allowed {
		if isBlockedWebhookAddr(netip.MustParseAddr(raw)) {
			t.Errorf("isBlockedWebhookAddr(%s) = true, want false", raw)
		}
	}
	if !isBlockedWebhookAddr(netip.Addr{}) {
		t.Error("the zero address must be treated as blocked")
	}
}

func TestWebhookHTTPClientRefusesLoopbackAtDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newWebhookHTTPClient(5 * time.Second)
	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("expected loopback delivery to be refused at dial time")
	}
	if !strings.Contains(err.Error(), errWebhookBlockedAddr.Error()) {
		t.Fatalf("error = %v, want a blocked-address dial failure", err)
	}
}

func TestWebhookHTTPClientRefusesRedirects(t *testing.T) {
	client := newWebhookHTTPClient(5 * time.Second)
	if client.CheckRedirect == nil {
		t.Fatal("webhook client must set a CheckRedirect policy")
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, errWebhookRedirect) {
		t.Fatalf("CheckRedirect err = %v, want errWebhookRedirect", err)
	}
}

func TestWebhookInputRejectsSSRFTargets(t *testing.T) {
	for name, raw := range map[string]string{
		"loopback ip":   "http://127.0.0.1/hook",
		"loopback name": "http://localhost/hook",
		"sub localhost": "http://api.localhost/hook",
		"private ip":    "http://10.1.2.3/hook",
		"metadata ip":   "http://169.254.169.254/latest/meta-data",
		"ipv6 loopback": "http://[::1]/hook",
		"cgnat ip":      "http://100.64.0.1/hook",
	} {
		input := webhookInput{URL: raw, EventTypes: []string{"*"}}
		if err := input.normalizeAndValidate(); err == nil {
			t.Errorf("SSRF target %q (%s) was accepted", name, raw)
		}
	}
	valid := webhookInput{URL: "https://hooks.example.com/ingest", EventTypes: []string{"*"}}
	if err := valid.normalizeAndValidate(); err != nil {
		t.Fatalf("public https webhook rejected: %v", err)
	}
}
