# Foreground live updates

The existing Go SSE bus sends small invalidations and fifteen-second heartbeats. It drops events for slow consumers and offers no durable replay cursor. The phone therefore re-fetches snapshots; an SSE payload never directly mutates authoritative transfer state.

## Transport

`DebridAPIClient.events()` uses a separate ephemeral session with the same immutable server/prefix and explicit credential. Cookie/credential stores, default headers, caches and redirects remain disabled. The request is `/api/v1/events` with an Accept header; no API key or session is placed in a query. Cancellation of the consuming task cancels the underlying network task.

The parser handles fragmented UTF-8, one leading BOM, LF/CRLF/CR, comments, multiline/empty data and blank-line dispatch. An incomplete EOF event is discarded. It deliberately rejects invalid completed UTF-8 and caps both a line and accumulated frame at64KiB (configurable parser ceiling256KiB). Comments do not accumulate the frame budget; multiple data/field lines do. The consumer retains only event names, discards payloads, ignores heartbeat frames and buffers at most16 invalidation hints. Dropped hints are safe because reconciliation re-fetches current state.

The no-data timeout is45seconds, exceeding the server heartbeat interval; resource lifetime is bounded at30minutes and reconnects normally afterward. Apple documents that [request timeout resets on arriving data](https://developer.apple.com/documentation/foundation/urlsessionconfiguration/timeoutintervalforrequest) while [resource timeout covers the entire transfer](https://developer.apple.com/documentation/foundation/urlsessionconfiguration/timeoutintervalforresource). Each URLSession [copies its configuration](https://developer.apple.com/documentation/foundation/urlsession/configuration), so extending event timeouts does not change normal JSON request limits. Framing follows the applicable [SSE rules](https://html.spec.whatwg.org/multipage/server-sent-events.html); no replay-ID behavior is claimed.

## Ownership and reconciliation

One active `TransferListModel` owns one event loop, one periodic resync and at most one scheduled refresh. Scene inactivity/disappearance cancels those tasks and the current GET, advances epochs/revisions and retains only its last successful snapshot. Repeated activation does not create another stream. A new foreground activation schedules an immediate full reconciliation and a fresh stream; connection establishment/re-establishment also invalidates the snapshot.

Change bursts coalesce for350ms. A change arriving during an in-flight GET sets a pending flag; it never repeatedly cancels that request. Completion schedules one follow-up refresh. Explicit pull refresh can replace an older GET as before. A45-second foreground resync covers missed events, slow-consumer drops and unavailable streaming.

Network disconnection/EOF retries with exponential delay,0.8–1.2 jitter, ordinary cap30seconds. A healthy thirty-second connection resets escalation; a flapping immediately-closed connection does not. HTTP Retry-After is honored up to the existing24-hour transport bound. Configuration/permission/framing failures stop stream retries for that activation and retain periodic snapshots. A401 stops live work and requests scoped session cleanup. Epoch, request revision, caller cancellation and current-server checks reject obsolete responses before state/auth changes.

The interface reports connection/reconnection/periodic/paused state without claiming that an open stream proves a fresh snapshot. Snapshot timestamps remain authoritative. No background modes, polling clones, notifications or provider probes are added.

## Verification

Pure parser fixtures cover byte fragmentation, Unicode/BOM, mixed newlines, comments, empty/multiline data, EOF, malformed UTF-8 and line/frame limits. A manually controlled clock verifies foreground activation/resume, coalescing during a held GET, retry/backoff, fallback and401. URLProtocol tests exercise actual framed bytes, prefix-bound header credentials, heartbeat filtering, wrong MIME/status/oversize frames, refused redirect and cancellation of a held-open connection. Production events/transfers are not mutated for validation.

Final local checks:74 core Swift Testing functions and seven native UI tests passed on the shared iOS27.0 simulator, including the strengthened pre-cancellation redirect observation. Unsigned arm64 app/framework compilation and linking passed separately. Independent source/evidence review approved with no remaining blocker. No simulator, runtime or worktree was added.
