package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// webhookDialTimeout bounds the TCP connect for a single webhook delivery.
const webhookDialTimeout = 10 * time.Second

// webhookMaxResponseBytes caps how much of an endpoint's response body we drain;
// receivers are expected to ack with a small body, so this is only a safety net
// against a hostile endpoint streaming megabytes back at the worker.
const webhookMaxResponseBytes = 64 * 1024

// errWebhookBlockedAddr rejects a webhook target that resolves to a non-public
// address at connect time. Vetting the resolved socket address (not the hostname)
// is the DNS-rebinding-proof SSRF boundary.
var errWebhookBlockedAddr = errors.New("webhook target resolves to a blocked address")

// errWebhookRedirect refuses any HTTP redirect on a webhook delivery, so a 3xx
// can never bounce a signed payload to an attacker-chosen or internal host.
var errWebhookRedirect = errors.New("webhook redirects are not allowed")

// blockedWebhookPrefixes are ranges a tenant webhook may never reach but that
// netip's own predicates do not cover: carrier-grade NAT, IETF protocol
// assignments, benchmarking, documentation, and reserved/future-use blocks.
// Loopback, private (RFC 1918 / IPv6 ULA), link-local, multicast, and the
// unspecified address are handled by the predicate calls in isBlockedWebhookAddr.
var blockedWebhookPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 carrier-grade NAT / internal meshes
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1 documentation
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2 documentation
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3 documentation
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved / future use
	netip.MustParsePrefix("100::/64"),        // IPv6 discard-only
	netip.MustParsePrefix("2001:db8::/32"),   // IPv6 documentation
}

// isBlockedWebhookAddr reports whether addr is anything other than a public
// unicast address. It unmaps IPv4-in-IPv6 first so ::ffff:127.0.0.1 and
// ::ffff:169.254.169.254 are judged as their embedded IPv4 address. The cloud
// metadata endpoint (169.254.169.254) is link-local unicast, so it is caught by
// IsLinkLocalUnicast without a dedicated prefix.
func isBlockedWebhookAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return true
	}
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, prefix := range blockedWebhookPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// webhookDialControl runs after DNS resolution with the concrete remote address,
// so it blocks loopback, private, link-local, metadata, and other non-public
// targets even when a public hostname rebinds to an internal IP.
func webhookDialControl(_, address string, _ syscall.RawConn) error {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("parse webhook dial address %q: %w", address, err)
	}
	if isBlockedWebhookAddr(addrPort.Addr()) {
		return fmt.Errorf("%w: %s", errWebhookBlockedAddr, addrPort.Addr())
	}
	return nil
}

// newWebhookHTTPClient builds the delivery client. It never honors ambient proxy
// settings, refuses redirects, and vets every resolved socket address against
// isBlockedWebhookAddr in the dialer's Control hook — after DNS resolution but
// before connect(2) — so a hostname that rebinds to a private IP is still caught.
func newWebhookHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: webhookDialTimeout, Control: webhookDialControl}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errWebhookRedirect
		},
	}
}

// webhookWorker drains the transactional outbox and delivers signed events to registered
// endpoints. Delivery is at-least-once: receivers dedupe on the event id. Claims are atomic,
// so multiple instances can run safely.
type webhookWorker struct {
	app         *application
	client      *http.Client
	interval    time.Duration
	claimTTL    time.Duration
	baseBackoff time.Duration
	maxBackoff  time.Duration
	maxAttempts int
	batchSize   int
	stop        chan struct{}
	done        chan struct{}
}

func newWebhookWorker(app *application) *webhookWorker {
	return &webhookWorker{
		app:         app,
		client:      newWebhookHTTPClient(10 * time.Second),
		interval:    app.config.WebhookPollInterval,
		claimTTL:    time.Minute,
		baseBackoff: 10 * time.Second,
		maxBackoff:  time.Hour,
		maxAttempts: app.config.WebhookMaxAttempts,
		batchSize:   50,
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

func (worker *webhookWorker) Start() {
	go worker.run()
}

func (worker *webhookWorker) Stop(ctx context.Context) {
	close(worker.stop)
	select {
	case <-worker.done:
	case <-ctx.Done():
	}
}

func (worker *webhookWorker) run() {
	defer close(worker.done)
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		select {
		case <-worker.stop:
			return
		case <-ticker.C:
			worker.drain(context.Background())
		}
	}
}

func (worker *webhookWorker) drain(ctx context.Context) {
	for processed := 0; processed < worker.batchSize; processed++ {
		select {
		case <-worker.stop:
			return
		default:
		}
		event, claimed, err := worker.claimNext(ctx)
		if err != nil {
			worker.app.logger.Error("webhook claim failed", "error", err)
			return
		}
		if !claimed {
			return
		}
		worker.deliver(ctx, event)
	}
}

func (worker *webhookWorker) claimNext(ctx context.Context) (outboxEvent, bool, error) {
	now := time.Now().UTC()
	filter := bson.M{
		"delivery.state":       bson.M{"$in": bson.A{"pending", "retry", "sending"}},
		"delivery.availableAt": bson.M{"$lte": now},
	}
	update := bson.M{"$set": bson.M{"delivery.state": "sending", "delivery.availableAt": now.Add(worker.claimTTL)}}
	findOptions := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "delivery.availableAt", Value: 1}, {Key: "_id", Value: 1}}).
		SetReturnDocument(options.After)
	var event outboxEvent
	err := worker.app.database.Collection(outboxCollectionName).FindOneAndUpdate(ctx, filter, update, findOptions).Decode(&event)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return outboxEvent{}, false, nil
	}
	if err != nil {
		return outboxEvent{}, false, err
	}
	return event, true, nil
}

func (worker *webhookWorker) deliver(ctx context.Context, event outboxEvent) {
	endpoints, err := worker.matchingEndpoints(ctx, event.AppID, event.Type)
	if err != nil {
		worker.app.logger.Error("webhook endpoint lookup failed", "error", err, "eventId", event.ID.Hex())
		worker.reschedule(ctx, event, "endpoint lookup failed")
		return
	}
	if len(endpoints) == 0 {
		worker.setState(ctx, event.ID, deliveredOutboxUpdate(time.Now().UTC()))
		return
	}
	payload, err := json.Marshal(event)
	if err != nil {
		worker.app.logger.Error("webhook payload marshal failed", "error", err, "eventId", event.ID.Hex())
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "dead"}})
		return
	}
	delivered := true
	for _, endpoint := range endpoints {
		if err := worker.post(ctx, endpoint, event, payload); err != nil {
			delivered = false
			worker.app.logger.Warn("webhook delivery failed", "error", err, "url", endpoint.URL, "eventId", event.ID.Hex())
		}
	}
	if delivered {
		worker.setState(ctx, event.ID, deliveredOutboxUpdate(time.Now().UTC()))
		return
	}
	worker.reschedule(ctx, event, "delivery failed")
}

func (worker *webhookWorker) matchingEndpoints(ctx context.Context, appIDHex, eventType string) ([]webhookEndpoint, error) {
	appID, err := bson.ObjectIDFromHex(appIDHex)
	if err != nil {
		return nil, nil
	}
	cursor, err := worker.app.database.Collection(webhookCollectionName).Find(ctx, bson.M{"appId": appID, "active": true})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var endpoints []webhookEndpoint
	if err := cursor.All(ctx, &endpoints); err != nil {
		return nil, err
	}
	matched := make([]webhookEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if matchesEventType(endpoint.EventTypes, eventType) {
			matched = append(matched, endpoint)
		}
	}
	return matched, nil
}

func (worker *webhookWorker) post(ctx context.Context, endpoint webhookEndpoint, event outboxEvent, payload []byte) error {
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.URL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-M-Stash-Event-Id", event.ID.Hex())
	request.Header.Set("X-M-Stash-Event-Type", event.Type)
	request.Header.Set("X-M-Stash-Signature", signWebhookPayload(endpoint.Secret, payload))
	response, err := worker.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, webhookMaxResponseBytes))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned status %d", response.StatusCode)
	}
	return nil
}

func (worker *webhookWorker) reschedule(ctx context.Context, event outboxEvent, reason string) {
	attempts := event.Delivery.Attempts + 1
	if attempts >= worker.maxAttempts {
		worker.app.logger.Error("webhook event dead-lettered", "eventId", event.ID.Hex(), "attempts", attempts, "reason", reason)
		worker.setState(ctx, event.ID, bson.M{"$set": bson.M{"delivery.state": "dead", "delivery.attempts": attempts}})
		return
	}
	availableAt := time.Now().UTC().Add(webhookBackoff(attempts, worker.baseBackoff, worker.maxBackoff))
	worker.setState(ctx, event.ID, bson.M{"$set": bson.M{
		"delivery.state":       "retry",
		"delivery.attempts":    attempts,
		"delivery.availableAt": availableAt,
	}})
}

func (worker *webhookWorker) setState(ctx context.Context, id bson.ObjectID, update bson.M) {
	if _, err := worker.app.database.Collection(outboxCollectionName).UpdateByID(ctx, id, update); err != nil {
		worker.app.logger.Error("webhook state update failed", "error", err, "eventId", id.Hex())
	}
}

// deliveredOutboxUpdate marks an event delivered and stamps delivery.expiresAt so
// the TTL index reclaims it after outboxDeliveredTTL. Undelivered states never
// carry that field, so only delivered rows are ever auto-expired.
func deliveredOutboxUpdate(now time.Time) bson.M {
	return bson.M{"$set": bson.M{
		"delivery.state":       "delivered",
		"delivery.availableAt": now,
		"delivery.expiresAt":   now.Add(outboxDeliveredTTL),
	}}
}
