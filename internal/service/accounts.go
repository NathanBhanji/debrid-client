package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// AccountView is an account with secrets stripped plus live user info when known.
type AccountView struct {
	domain.ProviderAccount
	User *provider.User `json:"user,omitempty"`
}

// Redact removes secrets for display.
func (a AccountView) Redact() AccountView {
	a.Credentials = domain.Credentials{}
	return a
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
		return AccountView{}, err
	}
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	return AccountView{ProviderAccount: acc, User: user}, nil
}

// ListAccounts returns all accounts (secrets included; callers redact).
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
		out = append(out, AccountView{ProviderAccount: a})
	}
	return out, nil
}

// GetAccount returns one account by id or name.
func (s *Service) GetAccount(ctx context.Context, idOrName string) (AccountView, error) {
	row, err := s.store.GetProviderAccount(ctx, idOrName)
	if store.IsNotFound(err) {
		row, err = s.store.GetProviderAccountByName(ctx, idOrName)
	}
	if err != nil {
		if store.IsNotFound(err) {
			return AccountView{}, fmt.Errorf("%w: account %q", ErrNotFound, idOrName)
		}
		return AccountView{}, err
	}
	a, err := store.AccountFromRow(row)
	if err != nil {
		return AccountView{}, err
	}
	return AccountView{ProviderAccount: a}, nil
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

// UpdateAccount changes name/credentials/enabled/default.
func (s *Service) UpdateAccount(ctx context.Context, idOrName string, in UpdateAccountInput) (AccountView, error) {
	cur, err := s.GetAccount(ctx, idOrName)
	if err != nil {
		return AccountView{}, err
	}
	acc := cur.ProviderAccount
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" && *in.Name != acc.Name {
		if _, err := s.store.GetProviderAccountByName(ctx, *in.Name); err == nil {
			return AccountView{}, fmt.Errorf("%w: account name %q already exists", ErrConflict, *in.Name)
		}
		acc.Name = strings.TrimSpace(*in.Name)
	}
	if in.Credentials != nil {
		prov, err := s.providers.Build(acc.Kind, *in.Credentials)
		if err != nil {
			return AccountView{}, fmt.Errorf("%w: %w", ErrValidation, err)
		}
		if !in.SkipVerify {
			if _, err := prov.User(ctx); err != nil {
				return AccountView{}, fmt.Errorf("%w: provider rejected credentials: %w", ErrValidation, err)
			}
		}
		acc.Credentials = *in.Credentials
	}
	if in.Enabled != nil {
		acc.Enabled = *in.Enabled
	}
	acc.UpdatedAt = s.now()
	params, err := store.AccountUpdateParams(acc)
	if err != nil {
		return AccountView{}, err
	}
	err = s.store.WithTx(ctx, func(q *sqlcgen.Queries) error {
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
		return AccountView{}, err
	}
	s.providers.Invalidate(acc.ID)
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	return AccountView{ProviderAccount: acc}, nil
}

// DeleteAccount removes an account. Fails with ErrConflict while torrents
// reference it unless force is set, in which case those torrents are deleted
// locally (not at the provider).
func (s *Service) DeleteAccount(ctx context.Context, idOrName string, force bool) error {
	acc, err := s.GetAccount(ctx, idOrName)
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
	if err := s.store.DeleteProviderAccount(ctx, acc.ID); err != nil {
		return err
	}
	s.providers.Invalidate(acc.ID)
	// Promote another account to default if we removed the default.
	if acc.IsDefault {
		if rest, err := s.store.ListProviderAccounts(ctx); err == nil && len(rest) > 0 {
			_ = s.store.SetDefaultProviderAccount(ctx, sqlcgen.SetDefaultProviderAccountParams{UpdatedAt: store.FormatTime(s.now()), ID: rest[0].ID})
		}
	}
	s.events.Publish(events.Event{Type: events.AccountChanged, AccountID: acc.ID})
	return nil
}

// TestAccount calls the provider and returns the live user info.
func (s *Service) TestAccount(ctx context.Context, idOrName string) (provider.User, error) {
	acc, err := s.GetAccount(ctx, idOrName)
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
