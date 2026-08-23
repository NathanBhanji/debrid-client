package provider_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/provider/providertest"
)

func TestRegistry(t *testing.T) {
	kind := domain.ProviderKind("testkind")
	provider.Register(kind, func(_ domain.Credentials, _ provider.Options) (provider.Provider, error) {
		return providertest.New(kind), nil
	})
	p, err := provider.New(kind, domain.Credentials{}, provider.Options{})
	if err != nil || p.Kind() != kind {
		t.Fatalf("New: %v", err)
	}
	if _, err := provider.New("unknown", domain.Credentials{}, provider.Options{}); err == nil {
		t.Fatal("unknown kind should error")
	}
	found := false
	for _, k := range provider.Kinds() {
		if k == kind {
			found = true
		}
	}
	if !found {
		t.Fatal("Kinds should include registered kind")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register should panic")
		}
	}()
	provider.Register(kind, nil)
}

func TestErrorModel(t *testing.T) {
	e := provider.Errorf(provider.ErrRateLimited, "34", "slow down")
	if !e.Retryable() || provider.KindOf(e) != provider.ErrRateLimited {
		t.Fatal("rate limited should be retryable")
	}
	if !errors.Is(e, &provider.Error{Kind: provider.ErrRateLimited}) || errors.Is(e, &provider.Error{Kind: provider.ErrRateLimited, Code: "99"}) {
		t.Fatal("Is matching wrong")
	}
	if provider.KindOf(errors.New("plain")) != provider.ErrTransient || !provider.IsRetryable(errors.New("plain")) {
		t.Fatal("unclassified errors default to transient/retryable")
	}
	if provider.IsRetryable(provider.Errorf(provider.ErrPermanent, "", "x")) || provider.IsRetryable(provider.Errorf(provider.ErrAuth, "", "x")) {
		t.Fatal("permanent/auth must not be retryable")
	}
	if provider.Wrap(provider.ErrTransient, nil) != nil {
		t.Fatal("Wrap(nil) must be nil")
	}
}

func TestFakeLifecycle(t *testing.T) {
	ctx := context.Background()
	f := providertest.New(domain.ProviderTorBox)
	res, err := f.AddMagnet(ctx, "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=x")
	if err != nil || res.Hash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("add: %v %+v", err, res)
	}
	// Adding the same hash again returns the same id (provider-side dedupe).
	res2, _ := f.AddMagnet(ctx, "magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567")
	if res2.ID != res.ID {
		t.Fatal("expected dedupe by hash")
	}
	f.SetFiles(res.ID, []domain.File{{ID: "1", Path: "a.mkv", Size: 10}})
	f.Finish(res.ID, []provider.Link{{FileID: "1", Path: "a.mkv", Size: 10, URL: "http://cdn/a.mkv"}})
	ls, err := f.ListTorrents(ctx)
	if err != nil || len(ls) != 1 || ls[0].Status != domain.TorrentFinished || ls[0].Size != 10 {
		t.Fatalf("list: %v %+v", err, ls)
	}
	links, _ := f.Links(ctx, res.ID)
	if len(links) != 1 {
		t.Fatal("links")
	}
	d, _ := f.Unrestrict(ctx, links[0].URL)
	if d.URL != links[0].URL {
		t.Fatal("direct links provider should return identity")
	}
	if err := f.Delete(ctx, res.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetTorrent(ctx, res.ID); provider.KindOf(err) != provider.ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
	f.Err = provider.Errorf(provider.ErrAuth, "", "bad key")
	if _, err := f.ListTorrents(ctx); provider.KindOf(err) != provider.ErrAuth {
		t.Fatal("Err should be returned")
	}
	if f.Calls("ListTorrents") != 2 {
		t.Fatalf("calls = %d", f.Calls("ListTorrents"))
	}
}
