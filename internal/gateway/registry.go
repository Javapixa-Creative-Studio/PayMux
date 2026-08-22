package gateway

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Factory builds an adapter for a specific configured account.
type Factory func(acc *Account, client *http.Client) (Gateway, error)

// Registry maps gateway names to adapter factories.
//
// Adapters are built per account rather than per process because credentials
// and environment differ between accounts. They share one HTTP client so
// connection pooling is preserved (PRD §74).
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
	client    *http.Client
}

// NewRegistry builds a Registry using a properly configured HTTP client.
func NewRegistry(client *http.Client) *Registry {
	if client == nil {
		client = NewHTTPClient(30 * time.Second)
	}
	return &Registry{factories: make(map[string]Factory), client: client}
}

// Register adds an adapter factory. Registering a name twice replaces the
// earlier factory, which keeps test doubles easy to install.
func (r *Registry) Register(name string, factory Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[name] = factory
}

// Names lists the registered gateways in a stable order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Supports reports whether a gateway name has an adapter.
func (r *Registry) Supports(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.factories[name]
	return ok
}

// For builds the adapter for a configured account.
func (r *Registry) For(acc *Account) (Gateway, error) {
	if acc == nil {
		return nil, fmt.Errorf("gateway: no account supplied")
	}
	if !acc.Environment.Valid() {
		return nil, fmt.Errorf("gateway: account %s has an invalid environment %q", acc.ID, acc.Environment)
	}
	if !acc.Enabled {
		return nil, fmt.Errorf("gateway: account %s is disabled", acc.ID)
	}
	r.mu.RLock()
	factory, ok := r.factories[acc.Gateway]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("gateway: no adapter is registered for %q", acc.Gateway)
	}
	return factory(acc, r.client)
}

// CapabilitiesFor reports what an account's adapter can do, combining the
// adapter's own declaration with the capability interfaces it implements.
func CapabilitiesFor(g Gateway) Capabilities {
	if reporter, ok := g.(CapabilityReporter); ok {
		return reporter.Capabilities()
	}
	caps := Capabilities{Cancel: true, Expire: true}
	if _, ok := g.(RefundGateway); ok {
		caps.Refund = true
		caps.PartialRefund = true
	}
	if _, ok := g.(CheckoutGateway); ok {
		caps.Checkout = true
	}
	if _, ok := g.(SubscriptionGateway); ok {
		caps.Subscriptions = true
	}
	return caps
}

// NewHTTPClient builds the shared outbound HTTP client for gateway calls.
//
// Every timeout is set explicitly: a gateway that accepts a connection and
// then stalls must not be able to pin a PayMux worker indefinitely (PRD §74).
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}
