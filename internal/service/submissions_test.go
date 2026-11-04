package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/NathanBhanji/debrid-client/internal/domain"
	"github.com/NathanBhanji/debrid-client/internal/events"
	"github.com/NathanBhanji/debrid-client/internal/provider"
	"github.com/NathanBhanji/debrid-client/internal/store"
)

func TestSubmissionReplayTracksIntentBeforeMutableDefaultsAndDeletion(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	a := f.addAccount(t, "original")
	in := AddTorrentInput{Magnet: magnetA + "&tr=https%3a%2f%2ffixture.test%2fa%3ftoken%3dprivate", SubmissionKey: uuid.NewString()}
	first, err := f.svc.AddTorrent(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.svc.GetTorrentSubmission(ctx, in.SubmissionKey)
	if err != nil || receipt.TorrentID != first.Torrent.ID || !receipt.TorrentExists {
		t.Fatalf("receipt %+v %v", receipt, err)
	}
	newDefault := f.addAccount(t, "new-default")
	if _, err := f.svc.UpdateAccount(ctx, newDefault.ID, UpdateAccountInput{SetDefault: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.UpdateAccount(ctx, a.ID, UpdateAccountInput{Enabled: ptr(false), Name: ptr("renamed")}); err != nil {
		t.Fatal(err)
	}
	defaults := DefaultSettings()
	defaults.TorrentDefaults.Unpack = false
	if _, err := f.svc.UpdateSettings(ctx, defaults); err != nil {
		t.Fatal(err)
	}
	// A restart is a new service using the same durable database, without in-memory dedupe state.
	restarted := New(f.store, f.svc.providers, f.eng, events.New(), Config{}, nil)
	in.SubmissionKey = "\"" + strings.ToUpper(in.SubmissionKey) + "\""
	replay, err := restarted.AddTorrent(ctx, in)
	if err != nil || !replay.SubmissionReplayed || replay.Torrent.ID != first.Torrent.ID || replay.Torrent.AccountID != a.ID || !replay.Torrent.Settings.Unpack || f.eng.wakes != 1 {
		t.Fatalf("replay %+v %v wakes=%d", replay, err, f.eng.wakes)
	}
	for _, change := range []func(*AddTorrentInput){func(x *AddTorrentInput) { x.Magnet += "&dn=Changed" }, func(x *AddTorrentInput) { x.Category = "Changed" }, func(x *AddTorrentInput) { x.Account = a.ID }, func(x *AddTorrentInput) { settings := domain.DefaultTorrentSettings(); x.Settings = &settings }} {
		changed := in
		change(&changed)
		_, err := restarted.AddTorrent(ctx, changed)
		var conflict *TorrentConflict
		if !errors.As(err, &conflict) || conflict.Code != SubmissionMismatch || !errors.Is(err, ErrValidation) {
			t.Fatalf("changed intent: %v", err)
		}
	}
	if err := f.store.DeleteTorrent(ctx, first.Torrent.ID); err != nil {
		t.Fatal(err)
	}
	receipt, err = restarted.GetTorrentSubmission(ctx, in.SubmissionKey)
	if err != nil || receipt.TorrentExists {
		t.Fatalf("removed receipt %+v %v", receipt, err)
	}
	_, err = restarted.AddTorrent(ctx, in)
	var conflict *TorrentConflict
	if !errors.As(err, &conflict) || conflict.Code != SubmissionRemoved || conflict.TorrentID != first.Torrent.ID {
		t.Fatalf("recreated removed submission: %v", err)
	}
	if count, _ := f.store.CountTorrents(ctx); count != 0 {
		t.Fatal("receipt replay recreated a torrent")
	}
}
func TestSubmissionInvalidAndFailedTransactionsLeaveNoReceipt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.addAccount(t, "main")
	for _, key := range []string{"bad", uuid.Nil.String(), uuid.Must(uuid.NewV7()).String(), " " + uuid.NewString(), uuid.NewString() + ", " + uuid.NewString()} {
		if _, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA, SubmissionKey: key}); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid key %v", err)
		}
	}
	key := uuid.NewString()
	in := AddTorrentInput{Magnet: magnetA, SubmissionKey: key, Category: "../unsafe"}
	if _, err := f.svc.AddTorrent(ctx, in); !errors.Is(err, ErrValidation) {
		t.Fatal(err)
	}
	if _, err := f.svc.GetTorrentSubmission(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	in.Category = ""
	if _, err := f.svc.AddTorrent(ctx, in); err != nil {
		t.Fatal(err)
	}
	// Force receipt insertion to fail after the torrent INSERT; both must roll back.
	if _, err := f.store.DB().ExecContext(ctx, `CREATE TRIGGER fail_submission BEFORE INSERT ON torrent_submissions BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	in.SubmissionKey = uuid.NewString()
	in.Magnet = strings.ReplaceAll(magnetA, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := f.svc.AddTorrent(ctx, in); err == nil {
		t.Fatal("injected failure accepted")
	}
	if count, _ := f.store.CountTorrents(ctx); count != 1 {
		t.Fatal("torrent escaped rolled-back submission")
	}
	if f.eng.wakes != 1 {
		t.Fatal("rollback woke engine")
	}
	if _, err := f.svc.GetTorrentSubmission(ctx, in.SubmissionKey); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
func secondSubmissionService(t *testing.T, f *fixture) *Service {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(f.dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(st, NewProviders(st, nil, provider.Options{}), nil, events.New(), Config{}, nil)
}
func TestIndependentHandlesSerializeSameHashAndSameKeyAdds(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "keyed"}[keyed], func(t *testing.T) {
			f := setup(t)
			f.addAccount(t, "main")
			other := secondSubmissionService(t, f)
			services := []*Service{New(f.store, f.svc.providers, nil, events.New(), Config{}, nil), other}
			in := AddTorrentInput{Magnet: magnetA}
			if keyed {
				in.SubmissionKey = uuid.NewString()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			type result struct {
				value TorrentDetail
				err   error
			}
			out := make(chan result, 16)
			start := make(chan struct{})
			for index := 0; index < 16; index++ {
				go func(index int) {
					<-start
					value, err := services[index%2].AddTorrent(ctx, in)
					out <- result{value, err}
				}(index)
			}
			close(start)
			accepted, replayed, conflicts := 0, 0, 0
			id := ""
			for range 16 {
				r := <-out
				switch {
				case r.err == nil:
					if r.value.SubmissionReplayed {
						replayed++
					} else {
						accepted++
					}
					if id != "" && id != r.value.Torrent.ID {
						t.Fatal("different receipt ids")
					}
					id = r.value.Torrent.ID
				case errors.Is(r.err, ErrConflict):
					conflicts++
				default:
					t.Fatal(r.err)
				}
			}
			if accepted != 1 || (keyed && replayed != 15) || (!keyed && conflicts != 15) {
				t.Fatalf("accepted=%d replayed=%d conflicts=%d", accepted, replayed, conflicts)
			}
			if count, _ := f.store.CountTorrents(ctx); count != 1 {
				t.Fatal("duplicate insert")
			}
		})
	}
}
func makeErroredSubmissionDownload(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	ctx := context.Background()
	f.addAccount(t, "main")
	first, err := f.svc.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA})
	if err != nil {
		t.Fatal(err)
	}
	tor := first.Torrent
	tor.Status = domain.TorrentError
	tor.Error = "fixture"
	params, _ := store.TorrentUpdateParams(tor)
	if err := f.store.UpdateTorrent(ctx, params); err != nil {
		t.Fatal(err)
	}
	d := domain.Download{ID: uuid.NewString(), TorrentID: tor.ID, ProviderLink: "fixture", RelPath: "fixture", Filename: "fixture", State: domain.DownloadError, Error: "fixture", QueuedAt: tor.AddedAt, UpdatedAt: tor.AddedAt}
	if _, err := f.store.InsertDownload(ctx, store.DownloadInsertParams(d)); err != nil {
		t.Fatal(err)
	}
	return tor.ID, d.ID
}
func TestAddVersusRetryActivationAndFailedFileRetryRollback(t *testing.T) {
	for _, fileRetry := range []bool{false, true} {
		t.Run(map[bool]string{false: "torrent", true: "file"}[fileRetry], func(t *testing.T) {
			f := setup(t)
			tor, download := makeErroredSubmissionDownload(t, f)
			other := secondSubmissionService(t, f)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			start := make(chan struct{})
			out := make(chan error, 2)
			go func() { <-start; _, err := other.AddTorrent(ctx, AddTorrentInput{Magnet: magnetA}); out <- err }()
			go func() {
				<-start
				var err error
				if fileRetry {
					_, err = f.svc.RetryDownload(ctx, download)
				} else {
					_, err = f.svc.RetryTorrent(ctx, tor)
				}
				out <- err
			}()
			close(start)
			a, b := <-out, <-out
			if (a == nil) == (b == nil) || (a != nil && !errors.Is(a, ErrConflict)) || (b != nil && !errors.Is(b, ErrConflict)) {
				t.Fatalf("race: %v / %v", a, b)
			}
			all, err := f.svc.ListTorrents(ctx, ListFilter{})
			if err != nil {
				t.Fatal(err)
			}
			active := 0
			for _, d := range all {
				if !d.Torrent.Status.IsTerminal() {
					active++
				}
			}
			if active != 1 {
				t.Fatal("multiple active copies")
			}
			current, err := f.svc.GetTorrent(ctx, tor)
			if err != nil {
				t.Fatal(err)
			}
			if fileRetry && current.Torrent.Status == domain.TorrentError {
				row, _ := f.store.GetDownload(ctx, download)
				if row.State != string(domain.DownloadError) || row.Error != "fixture" {
					t.Fatal("child reset escaped rejected parent activation")
				}
			}
		})
	}
}

// This golden forces deliberate fingerprint compatibility work when the settings model evolves.
func TestSubmissionFingerprintV1IsStable(t *testing.T) {
	settings := domain.DefaultTorrentSettings()
	in := AddTorrentInput{Magnet: magnetA, Settings: &settings, SubmissionKey: "00000000-0000-4000-8000-000000000000"}
	key, hash, err := submissionIdentity(in, domain.Torrent{PayloadKind: domain.PayloadMagnet, Payload: []byte(magnetA)})
	if err != nil || key != "db8055e0e0307d5a016bec4dc338d69875eb0fb7e614a8b125b08fb082095d98" || hash != "d6a457375d2d7eed06576b0c10f894a122ef009e89b5dfe144555234d56ff6f3" {
		t.Fatalf("submission fingerprint protocol changed: %s %s %v", key, hash, err)
	}
}
