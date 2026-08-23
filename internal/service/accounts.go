package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
	"github.com/NathanBhanji/debrid-client/internal/store/sqlcgen"
)

// AddAccountInput describes a new provider account.
type AddAccountInput struct {
	Kind        domain.ProviderKind
	Name        string
	Credentials domain.Credentials
	SetDefault  bool
	// SkipVerify skips the live credential check (tests / offline setup).
	SkipVerify bool
}

// AccountView is the public shape of an account: never carries secrets, so it
// is safe to serialise from any surface (API/CLI/MCP). Internal code that
// needs credentials uses domain.ProviderAccount via account().
type AccountView struct {
	ID             string              `json:"id"`
	Kind           domain.ProviderKind `json:"kind"`
	Name           string              `json:"name"`
	Enabled        bool                `json:"enabled"`
	IsDefault      bool                `json:"is_default"`
	HasCredentials bool                `json:"has_credentials"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
	User           *provider.User      `json:"user,omitempty"`
}

func viewOf(a domain.ProviderAccount) AccountView {
	return AccountView{
		ID: a.ID, Kind: a.Kind, Name: a.Name, Enabled: a.Enabled, IsDefault: a.IsDefault,
		HasCredentials: a.Credentials.APIKey != "" || a.Credentials.AccessToken != "",
		CreatedAt:      a.CreatedAt, UpdatedAt: a.UpdatedAt,
	}
}

// AddAccount validates credentials against the provider and stores the account.
// The first account becomes the default automatically.
func (s *Service) AddAccount(ctx context.Context, in AddAccountInput) (AccountView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !in.Kind.Valid() {
		return AccountView{}, validationErr("unknown provider kind %q", in.Kind)
	}
	if in.Name == "" {
		in.Name = string(in.Kind)
	}
	if _, err := s.store.GetProviderAccountByName(ctx, in.Name); err == nil {
		return AccountView{}, fmt.Errorf("%w: account name %q already exists", ErrConflict, in.Name)
	}
	if in.Credentials.APIKey == "" && in.Credentials.AccessToken == "" {
		return AccountView{}, validationErr("credentials are required")
	}
	prov, err := s.providers.Build(in.Kind, in.Credentials)
	if err != nil {
		return AccountView{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	var user *provider.User
	if !in.SkipVerify {
		u, err := prov.User(ctx)
		if err != nil {
			return AccountView{}, fmt.Errorf("%w: provider rejected credentials: %w", ErrValidation, err)
		}
		user = &u
	}
	now := s.now()
	acc := domain.ProviderAccount{
		ID: s.newID(), Kind: in.Kind, Name: in.Name, Credentials: in.Credentials, Enabled: true,
		CreatedAt: now, UpdatedAt: now,
	}
	existing, err := s.store.ListProviderAccounts(ctx)
	if err != nil {
		return AccountView{}, err
	}
	acc.IsDefault = in.SetDefault || len(existing) == 0
	params, err := store.AccountInsertParams(acc)
	if err != nil {
		return AccountView{}, err
	}
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if acc.IsDefault {
			if err := q.ClearDefaultProviderAccount(ctx, store.FormatTime(now)); err != nil {
				return err
			}
		}
		return q.InsertProviderAccount(ctx, params)
	})
	if err != nil {
		return AccountView{}, uniqueToConflict(err, "account name %q already exists", in.Name)
	}
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	v := viewOf(acc)
	v.User = user
	return v, nil
}

// uniqueToConflict maps a SQLite UNIQUE violation (check-then-insert race) to ErrConflict.
func uniqueToConflict(err error, format string, args ...any) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
	}
	return err
}

// ListAccounts returns all accounts (no secrets).
func (s *Service) ListAccounts(ctx context.Context) ([]AccountView, error) {
	rows, err := s.store.ListProviderAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, 0, len(rows))
	for _, r := range rows {
		a, err := store.AccountFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, viewOf(a))
	}
	return out, nil
}

// GetAccount returns one account by id or name (no secrets).
func (s *Service) GetAccount(ctx context.Context, idOrName string) (AccountView, error) {
	a, err := s.account(ctx, idOrName)
	if err != nil {
		return AccountView{}, err
	}
	return viewOf(a), nil
}

// account loads the full account (with credentials) by id or name.
func (s *Service) account(ctx context.Context, idOrName string) (domain.ProviderAccount, error) {
	row, err := s.store.GetProviderAccount(ctx, idOrName)
	if store.IsNotFound(err) {
		row, err = s.store.GetProviderAccountByName(ctx, idOrName)
	}
	if err != nil {
		if store.IsNotFound(err) {
			return domain.ProviderAccount{}, fmt.Errorf("%w: account %q", ErrNotFound, idOrName)
		}
		return domain.ProviderAccount{}, err
	}
	return store.AccountFromRow(row)
}

// DefaultAccount returns the default account, or ErrNotFound if none configured.
func (s *Service) DefaultAccount(ctx context.Context) (domain.ProviderAccount, error) {
	row, err := s.store.GetDefaultProviderAccount(ctx)
	if err != nil {
		if store.IsNotFound(err) {
			return domain.ProviderAccount{}, fmt.Errorf("%w: no default provider account configured", ErrNotFound)
		}
		return domain.ProviderAccount{}, err
	}
	return store.AccountFromRow(row)
}

// UpdateAccountInput holds optional changes.
type UpdateAccountInput struct {
	Name        *string
	Credentials *domain.Credentials
	Enabled     *bool
	SetDefault  bool
	SkipVerify  bool
}

// UpdateAccount changes name/credentials/enabled/default. Credentials are
// verified with the provider (unless SkipVerify) before the row is touched;
// the read-modify-write itself happens inside one transaction.
func (s *Service) UpdateAccount(ctx context.Context, idOrName string, in UpdateAccountInput) (AccountView, error) {
	cur, err := s.account(ctx, idOrName)
	if err != nil {
		return AccountView{}, err
	}
	var newName string
	if in.Name != nil {
		newName = strings.TrimSpace(*in.Name)
		if newName != "" && newName != cur.Name {
			if _, err := s.store.GetProviderAccountByName(ctx, newName); err == nil {
				return AccountView{}, fmt.Errorf("%w: account name %q already exists", ErrConflict, newName)
			}
		}
	}
	if in.Credentials != nil {
		if in.Credentials.APIKey == "" && in.Credentials.AccessToken == "" {
			return AccountView{}, validationErr("credentials are required")
		}
		prov, err := s.providers.Build(cur.Kind, *in.Credentials)
		if err != nil {
			return AccountView{}, fmt.Errorf("%w: %w", ErrValidation, err)
		}
		if !in.SkipVerify {
			if _, err := prov.User(ctx); err != nil {
				return AccountView{}, fmt.Errorf("%w: provider rejected credentials: %w", ErrValidation, err)
			}
		}
	}
	var acc domain.ProviderAccount
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetProviderAccount(ctx, cur.ID)
		if err != nil {
			return err
		}
		acc, err = store.AccountFromRow(row)
		if err != nil {
			return err
		}
		if newName != "" {
			acc.Name = newName
		}
		if in.Credentials != nil {
			acc.Credentials = *in.Credentials
		}
		if in.Enabled != nil {
			acc.Enabled = *in.Enabled
		}
		acc.UpdatedAt = s.now()
		params, err := store.AccountUpdateParams(acc)
		if err != nil {
			return err
		}
		if err := q.UpdateProviderAccount(ctx, params); err != nil {
			return err
		}
		if in.SetDefault && !acc.IsDefault {
			ts := store.FormatTime(acc.UpdatedAt)
			if err := q.ClearDefaultProviderAccount(ctx, ts); err != nil {
				return err
			}
			if err := q.SetDefaultProviderAccount(ctx, sqlcgen.SetDefaultProviderAccountParams{UpdatedAt: ts, ID: acc.ID}); err != nil {
				return err
			}
			acc.IsDefault = true
		}
		return nil
	})
	if err != nil {
		return AccountView{}, uniqueToConflict(err, "account name %q already exists", newName)
	}
	s.providers.Invalidate(acc.ID)
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	return viewOf(acc), nil
}

// DeleteAccount removes an account. Fails with ErrConflict while torrents
// reference it unless force is set, in which case those torrents are deleted
// locally (not at the provider).
func (s *Service) DeleteAccount(ctx context.Context, idOrName string, force bool) error {
	acc, err := s.account(ctx, idOrName)
	if err != nil {
		return err
	}
	n, err := s.store.CountTorrentsForAccount(ctx, acc.ID)
	if err != nil {
		return err
	}
	if n > 0 && !force {
		return fmt.Errorf("%w: account has %d torrents; delete them first or use force", ErrConflict, n)
	}
	if n > 0 {
		rows, err := s.store.ListTorrentsByAccount(ctx, acc.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			_ = s.engine.CancelTorrent(ctx, r.ID)
			if err := s.store.DeleteTorrent(ctx, r.ID); err != nil {
				return err
			}
			s.events.Publish(events.Event{Type: events.TorrentDeleted, TorrentID: r.ID})
		}
	}
	// Delete and, if it was the default, promote the oldest remaining account — atomically.
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
		if err := q.DeleteProviderAccount(ctx, acc.ID); err != nil {
			return err
		}
		if !acc.IsDefault {
			return nil
		}
		rest, err := q.ListProviderAccounts(ctx)
		if err != nil || len(rest) == 0 {
			return err
		}
		return q.SetDefaultProviderAccount(ctx, sqlcgen.SetDefaultProviderAccountParams{UpdatedAt: store.FormatTime(s.now()), ID: rest[0].ID})
	})
	if err != nil {
		return err
	}
	s.providers.Invalidate(acc.ID)
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	return nil
}

// TestAccount calls the provider and returns the live user info.
func (s *Service) TestAccount(ctx context.Context, idOrName string) (provider.User, error) {
	acc, err := s.account(ctx, idOrName)
	if err != nil {
		return provider.User{}, err
	}
	prov, _, err := s.providers.For(ctx, acc.ID)
	if err != nil {
		return provider.User{}, err
	}
	u, err := prov.User(ctx)
	if err != nil {
		var pe *provider.Error
		if errors.As(err, &pe) {
			return provider.User{}, fmt.Errorf("%w: %s", ErrValidation, pe.Message)
		}
		return provider.User{}, err
	}
	return u, nil
}
