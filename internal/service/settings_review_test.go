package service

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func TestSettingsReviewDefaultsAreReadOnlyAndLegacyABAInvalidates(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.svc.now = func() time.Time { return time.Unix(1, 0).UTC() }
	original, err := f.svc.GetSettingsReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.GetSettingsReview(ctx)
	if err != nil || again.Revision != original.Revision {
		t.Fatal("unstable default review", err)
	}
	rows, err := f.store.ListSettings(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatal("review wrote settings", err)
	}
	changed := original.Settings
	changed.Categories = []string{" TV ", "Movies", "TV"}
	first, err := f.svc.UpdateSettingsReviewed(ctx, changed, original.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == original.Revision || !reflect.DeepEqual(first.Settings.Categories, []string{"Movies", "TV"}) {
		t.Fatal("normalization or revision missing")
	}
	if _, err = f.svc.UpdateSettings(ctx, original.Settings); err != nil {
		t.Fatal(err)
	}
	current, err := f.svc.GetSettingsReview(ctx)
	if err != nil || current.Revision == original.Revision || !reflect.DeepEqual(current.Settings, original.Settings) {
		t.Fatal("ABA review reused", err)
	}
	ch := f.svc.Events().Subscribe(ctx, 2)
	if _, err = f.svc.UpdateSettingsReviewed(ctx, changed, original.Revision); !errors.Is(err, ErrPrecondition) {
		t.Fatal("stale review accepted", err)
	}
	after, err := f.svc.GetSettingsReview(ctx)
	if err != nil || !reflect.DeepEqual(after, current) {
		t.Fatal("stale write changed settings", err)
	}
	select {
	case e := <-ch:
		t.Fatal("stale write published event", e.Type)
	default:
	}
	if err = f.svc.SetRaw(ctx, settingsRevisionKey, "overwrite"); !errors.Is(err, ErrValidation) {
		t.Fatal("revision key not reserved")
	}
}

func TestSettingsReviewRollbackAndMalformedMetadataFailClosed(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	view, err := f.svc.GetSettingsReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	view, err = f.svc.UpdateSettingsReviewed(ctx, view.Settings, view.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().ExecContext(ctx, `CREATE TRIGGER reject_review_nonce BEFORE UPDATE ON settings WHEN NEW.key='_settings_revision_v1' BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`); err != nil {
		t.Fatal(err)
	}
	ch := f.svc.Events().Subscribe(ctx, 2)
	update := view.Settings
	update.UnpackMaxDepth = 3
	if _, err = f.svc.UpdateSettingsReviewed(ctx, update, view.Revision); err == nil {
		t.Fatal("trigger did not fail")
	}
	got, err := f.svc.GetSettingsReview(ctx)
	if err != nil || !reflect.DeepEqual(got, view) {
		t.Fatal("partial value/nonce commit", err)
	}
	select {
	case e := <-ch:
		t.Fatal("failed transaction published", e.Type)
	default:
	}
	if _, err = f.store.DB().ExecContext(ctx, `DROP TRIGGER reject_review_nonce`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.DB().ExecContext(ctx, `UPDATE settings SET value='broken' WHERE key='_settings_revision_v1'`); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GetSettingsReview(ctx); err == nil {
		t.Fatal("corrupt metadata accepted")
	}
	if _, err = f.svc.UpdateSettingsReviewed(ctx, update, view.Revision); err == nil {
		t.Fatal("corrupt metadata overwritten conditionally")
	}
}

func TestSettingsReviewSerializesIndependentSQLiteWriters(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	second, err := store.Open(ctx, filepath.Join(f.dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	other := New(second, nil, nil, events.New(), Config{}, nil)
	view, err := f.svc.GetSettingsReview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*Service{f.svc, other} {
		wg.Add(1)
		go func(svc *Service, depth int) {
			defer wg.Done()
			<-start
			st := view.Settings
			st.UnpackMaxDepth = depth
			_, err := svc.UpdateSettingsReviewed(ctx, st, view.Revision)
			results <- err
		}(svc, i+2)
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrPrecondition):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("wanted one winner and stale refusal: %d/%d", success, conflict)
	}
}
