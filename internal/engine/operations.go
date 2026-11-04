package engine

import "context"

// operationSlot serializes admission for one torrent, without holding the engine
// job mutex while waiting for canceled workers to finish.
type operationSlot struct {
	token chan struct{}
	refs  int
}

// WithTorrentOperation freezes scheduler admission for this torrent while a
// service operation validates state, drains old jobs and commits its mutation.
func (e *Engine) WithTorrentOperation(ctx context.Context, id string, run func() error) error {
	return e.torrentOperation(ctx, id, run, true)
}

func (e *Engine) torrentOperation(ctx context.Context, id string, run func() error, wait bool) error {
	e.mu.Lock()
	if e.operations == nil {
		e.operations = make(map[string]*operationSlot)
	}
	slot := e.operations[id]
	if slot == nil {
		slot = &operationSlot{token: make(chan struct{}, 1)}
		slot.token <- struct{}{}
		e.operations[id] = slot
	}
	slot.refs++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		slot.refs--
		if slot.refs == 0 {
			delete(e.operations, id)
		}
		e.mu.Unlock()
	}()
	if wait {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-slot.token:
		}
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-slot.token:
		default:
			return nil
		}
	}
	defer func() { slot.token <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return run()
}
