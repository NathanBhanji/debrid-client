package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
)

func TestReviewedRemovalRejectsNewIncarnationBeforeAndAfterDrain(t *testing.T) {
	for _, stage := range []string{"before", "during"} {
		t.Run(stage, func(t *testing.T) {
			f, tor, _ := retryFixture(t)
			ctx := context.Background()
			review, err := f.svc.GetRemovalReview(ctx, tor.ID)
			if err != nil || !review.Exists {
				t.Fatal(review, err)
			}
			mutate := func(id string) error {
				_, err := f.store.MutateTorrent(ctx, id, func(t *domain.Torrent) error { t.RetryCount++; return nil })
				return err
			}
			if stage == "before" {
				if err := mutate(tor.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.eng.cancelFn = mutate
			}
			err = f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{ExpectedRevision: review.Revision, DeleteFromProvider: true, DeleteFiles: true})
			if !errors.Is(err, ErrPrecondition) {
				t.Fatal("changed incarnation removed", err)
			}
			if stage == "before" && len(f.eng.cancels) != 0 {
				t.Fatal("stale removal drained new work")
			}
			if f.fake.Calls("Delete") != 0 {
				t.Fatal("stale removal acted remotely")
			}
			if _, err := f.svc.GetTorrent(ctx, tor.ID); err != nil {
				t.Fatal("stale removal deleted record", err)
			}
		})
	}
}
func TestReviewedRemovalPartialCleanupCanOnlyReportRecordAfterFinalDelete(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	remote, err := f.fake.AddMagnet(ctx, magnetA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.ProviderID = remote.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.GetRemovalReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.DB().ExecContext(ctx, "CREATE TRIGGER fail_remove BEFORE DELETE ON torrents BEGIN SELECT RAISE(ABORT, 'fixture disk write failure'); END"); err != nil {
		t.Fatal(err)
	}
	opts := DeleteOptions{ExpectedRevision: view.Revision, DeleteFromProvider: true}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, opts); err == nil {
		t.Fatal("partial removal reported success")
	}
	if len(f.fake.IDs()) != 0 {
		t.Fatal("fixture did not perform partial provider cleanup")
	}
	current, err := f.svc.GetRemovalReview(ctx, tor.ID)
	if err != nil || !current.Exists || current.Revision != view.Revision {
		t.Fatal("partial cleanup falsely reported removal", current, err)
	}
	if _, err := f.store.DB().ExecContext(ctx, "DROP TRIGGER fail_remove"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, opts); err != nil {
		t.Fatal("explicit identical retry failed", err)
	}
	current, err = f.svc.GetRemovalReview(ctx, tor.ID)
	if err != nil || current.Exists || current.Revision != "" || current.ProviderCopyKnown {
		t.Fatal("absence not explicit", current, err)
	}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, opts); !errors.Is(err, ErrNotFound) {
		t.Fatal("duplicate removal did not report absent record", err)
	}
}
func TestReviewedRemovalDefaultKeepsFilesAndProviderAndRejectsAliases(t *testing.T) {
	f, tor, _ := retryFixture(t)
	ctx := context.Background()
	remote, err := f.fake.AddMagnet(ctx, magnetA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MutateTorrent(ctx, tor.ID, func(t *domain.Torrent) error { t.ProviderID = remote.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	dir := TorrentDir(f.svc.cfg.DownloadDir, tor)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "retained.mkv")
	if err := os.WriteFile(file, []byte("retained fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.GetRemovalReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteTorrent(ctx, tor.Hash, DeleteOptions{ExpectedRevision: view.Revision}); !errors.Is(err, ErrValidation) {
		t.Fatal("alias accepted", err)
	}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{ExpectedRevision: "bad"}); !errors.Is(err, ErrValidation) {
		t.Fatal("bad revision accepted", err)
	}
	if len(f.eng.cancels) != 0 {
		t.Fatal("invalid request stopped work")
	}
	if err := f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{ExpectedRevision: view.Revision}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "retained fixture" || len(f.fake.IDs()) != 1 {
		t.Fatal("safe defaults removed data", err)
	}
}
func TestCancellationInvalidatesPriorRemovalReview(t *testing.T) {
	f, tor, _ := cancellationFixture(t)
	ctx := context.Background()
	removal, err := f.svc.GetRemovalReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := f.svc.GetCancellationReview(ctx, tor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelReviewed(ctx, tor.ID, uuid.NewString(), cancel.Revision); err != nil {
		t.Fatal(err)
	}
	drains := len(f.eng.cancels)
	if err := f.svc.DeleteTorrent(ctx, tor.ID, DeleteOptions{ExpectedRevision: removal.Revision}); !errors.Is(err, ErrPrecondition) {
		t.Fatal("stale confirmation removed cancelled transfer", err)
	}
	if len(f.eng.cancels) != drains {
		t.Fatal("stale removal performed drain")
	}
}
