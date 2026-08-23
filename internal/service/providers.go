package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

// Factory constructs a provider; defaults to provider.New. Tests inject fakes.
type Factory func(kind domain.ProviderKind, creds domain.Credentials, opts provider.Options) (provider.Provider, error)

// Providers resolves a provider.Provider for an account id, caching instances
// until the account's credentials change.
type Providers struct {
	store   *store.Store
	factory Factory
	opts    provider.Options

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	updatedAt string
	p         provider.Provider
}

// NewProviders creates a resolver.
func NewProviders(st *store.Store, factory Factory, opts provider.Options) *Providers {
	if factory == nil {
		factory = provider.New
	}
	return &Providers{store: st, factory: factory, opts: opts, cache: map[string]cached{}}
}

// For returns the provider for an account, building it if needed.
func (p *Providers) For(ctx context.Context, accountID string) (provider.Provider, domain.ProviderAccount, error) {
	row, err := p.store.GetProviderAccount(ctx, accountID)
	if err != nil {
		if store.IsNotFound(err) {
			return nil, domain.ProviderAccount{}, fmt.Errorf("%w: account %s", ErrNotFound, accountID)
		}
		return nil, domain.ProviderAccount{}, err
	}
	acc, err := store.AccountFromRow(row)
	if err != nil {
		return nil, domain.ProviderAccount{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.cache[accountID]; ok && c.updatedAt == row.UpdatedAt {
		return c.p, acc, nil
	}
	prov, err := p.factory(acc.Kind, acc.Credentials, p.opts)
	if err != nil {
		return nil, acc, err
	}
	p.cache[accountID] = cached{updatedAt: row.UpdatedAt, p: prov}
	return prov, acc, nil
}

// Build constructs a provider without persisting anything (used to validate
// credentials before saving an account).
func (p *Providers) Build(kind domain.ProviderKind, creds domain.Credentials) (provider.Provider, error) {
	return p.factory(kind, creds, p.opts)
}

// Invalidate drops a cached provider.
func (p *Providers) Invalidate(accountID string) {
	p.mu.Lock()
	delete(p.cache, accountID)
	p.mu.Unlock()
}
