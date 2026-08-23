import { useEffect, useRef, useState } from 'react'

import { api } from '#/lib/api'
import { formatBytes, formatSpeed, formatWhen } from '#/lib/format'
import type { Torrent } from '#/lib/api'

const SEGMENTS = 12

type LedKind = 'q' | 'prov' | 'dl' | 'done' | 'err'

// Maps the provider-neutral lifecycle to the front-panel readout: grey while
// queued locally, amber while the provider works, blue while we download,
// green when done, red on error.
function ledFor(t: Torrent): {
  kind: LedKind
  label: string
  segClass: string
} {
  switch (t.status) {
    case 'queued':
    case 'adding':
      return { kind: 'q', label: '…', segClass: 'amb' }
    case 'processing':
    case 'downloading':
    case 'uploading':
      return { kind: 'prov', label: 'REM', segClass: 'amb' }
    case 'waiting_selection':
      return { kind: 'prov', label: 'SEL', segClass: 'amb' }
    case 'finished':
      return { kind: 'dl', label: 'DL', segClass: 'on' }
    case 'completed':
      return { kind: 'done', label: 'OK', segClass: 'grn' }
    case 'error':
      return { kind: 'err', label: 'ERR', segClass: 'red' }
    default: {
      // Unknown status = version skew (old binary + new UI or vice versa).
      // Render something neutral instead of crashing the whole app.
      t.status satisfies never
      return { kind: 'q', label: '?', segClass: 'amb' }
    }
  }
}

function progressFor(t: Torrent): number {
  if (t.status === 'completed') return 1
  if (t.status === 'finished') return t.local_progress
  // A local-phase error would otherwise show the provider's 100% in red,
  // which reads as "done"; show how far the local downloads actually got.
  if (t.status === 'error' && t.downloads.length > 0) return t.local_progress
  return t.provider_progress
}

function statusLine(t: Torrent): string {
  switch (t.status) {
    case 'queued':
      return 'queued'
    case 'adding':
      return 'sending to provider'
    case 'processing':
      return t.provider_status
        ? `provider: ${t.provider_status}`
        : 'provider processing'
    case 'waiting_selection':
      return 'waiting for file selection'
    case 'downloading':
      return t.speed > 0
        ? `provider ${formatSpeed(t.speed)}`
        : 'provider downloading'
    case 'uploading':
      return 'provider uploading'
    case 'finished':
      return 'downloading locally'
    case 'completed':
      return t.completed_at
        ? `completed ${formatWhen(t.completed_at)}`
        : 'completed'
    case 'error':
      return t.error ?? 'error'
    default: {
      t.status satisfies never
      return String(t.status)
    }
  }
}

function Segments({ t }: { t: Torrent }) {
  const { segClass } = ledFor(t)
  const p = progressFor(t)
  const lit = Math.round(p * SEGMENTS)
  const active = t.status !== 'completed' && t.status !== 'error'
  return (
    <div className="seg" aria-hidden="true">
      {Array.from({ length: SEGMENTS }, (_, i) => (
        <i
          key={i}
          className={
            i < lit
              ? segClass + (active && i === lit - 1 ? ' blink' : '')
              : undefined
          }
        />
      ))}
      <span className="pc">{Math.floor(p * 100)}%</span>
    </div>
  )
}

function downloadStateClass(state: string): string {
  switch (state) {
    case 'done':
      return 'ok'
    case 'error':
      return 'bad'
    case 'pending':
      return 'wait'
    default:
      return 'run'
  }
}

// Selection UI while the provider waits for file selection; otherwise a
// read-only listing of local downloads (with per-download retry) or files.
function FilePanel({
  t,
  busy,
  run,
}: {
  t: Torrent
  busy: boolean
  run: (p: Promise<unknown>) => void
}) {
  const selecting = t.status === 'waiting_selection'
  const [picked, setPicked] = useState<Set<string>>(
    () => new Set(t.files.filter((f) => f.selected).map((f) => f.id)),
  )
  const toggle = (id: string) =>
    setPicked((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })

  if (t.downloads.length > 0 && !selecting) {
    return (
      <div className="filelist">
        <div className="hd">LOCAL DOWNLOADS</div>
        {t.downloads.map((d) => (
          <div className="frow" key={d.id}>
            <span className="fp">{d.path}</span>
            <span className="fs">{formatBytes(d.size)}</span>
            {d.state === 'error' ? (
              <button
                className="fst bad frow-retry"
                disabled={busy}
                onClick={() => run(api.downloads.retry(d.id))}
              >
                retry
              </button>
            ) : (
              <span className={`fst ${downloadStateClass(d.state)}`}>
                {d.state === 'downloading'
                  ? `${Math.floor(d.progress * 100)}%`
                  : d.state}
              </span>
            )}
          </div>
        ))}
      </div>
    )
  }
  return (
    <div className="filelist">
      <div className="hd">
        {selecting ? 'SELECT FILES TO FETCH' : 'FILES AT PROVIDER'}
      </div>
      {t.files.map((f) => (
        <div className="frow" key={f.id}>
          {selecting ? (
            <label className="fp fsel">
              <input
                type="checkbox"
                checked={picked.has(f.id)}
                disabled={busy}
                onChange={() => toggle(f.id)}
              />
              {f.path}
            </label>
          ) : (
            <span className="fp">{f.path}</span>
          )}
          <span className="fs">{formatBytes(f.size)}</span>
          {!selecting && (
            <span className={`fst ${f.selected ? 'ok' : 'wait'}`}>
              {f.selected ? 'sel' : '—'}
            </span>
          )}
        </div>
      ))}
      {t.files.length === 0 && (
        <div className="frow">
          <span className="fp" style={{ color: 'var(--lcd-dim)' }}>
            no file listing yet
          </span>
        </div>
      )}
      {selecting && (
        <div style={{ marginTop: 10 }}>
          <button
            className="key sm org"
            disabled={busy || picked.size === 0}
            onClick={() => run(api.torrents.selectFiles(t.id, [...picked]))}
          >
            FETCH {picked.size} FILE{picked.size === 1 ? '' : 'S'}
          </button>
        </div>
      )}
    </div>
  )
}

function Bay({ t }: { t: Torrent }) {
  const [busy, setBusy] = useState(false)
  const [actErr, setActErr] = useState<string | null>(null)
  const [confirmEject, setConfirmEject] = useState(false)
  const [wipeFiles, setWipeFiles] = useState(false)
  const [wipeProvider, setWipeProvider] = useState(false)

  // Actions rely on SSE to refresh the list; Bay only tracks in-flight state.
  const run = (p: Promise<unknown>) => {
    setBusy(true)
    setActErr(null)
    p.catch((e: unknown) =>
      setActErr(e instanceof Error ? e.message : String(e)),
    ).finally(() => setBusy(false))
  }

  return (
    <div className="bay">
      <div className="cols">
        <FilePanel t={t} busy={busy} run={run} />
        <div className="bayside">
          <div>
            <div className="kv">
              <span>hash</span>
              <b>{t.hash}</b>
            </div>
            <div className="kv">
              <span>added</span>
              <b>{formatWhen(t.added_at)}</b>
            </div>
            {t.category ? (
              <div className="kv">
                <span>category</span>
                <b>{t.category}</b>
              </div>
            ) : null}
            {t.provider_status ? (
              <div className="kv">
                <span>provider status</span>
                <b>{t.provider_status}</b>
              </div>
            ) : null}
            {t.seeders > 0 && (
              <div className="kv">
                <span>seeders</span>
                <b>{t.seeders}</b>
              </div>
            )}
            {t.retry_count > 0 && (
              <div className="kv">
                <span>retries</span>
                <b>{t.retry_count}</b>
              </div>
            )}
            {t.status_reason ? (
              <div className="kv">
                <span>state</span>
                <b>{t.status_reason}</b>
              </div>
            ) : null}
            {t.error ? (
              <div className="kv">
                <span>error</span>
                <b style={{ color: '#c23a1c' }}>{t.error}</b>
              </div>
            ) : null}
          </div>
          <div className="bayacts">
            {(t.status === 'error' || t.status === 'completed') && (
              <button
                className="key sm"
                disabled={busy}
                onClick={() => run(api.torrents.retry(t.id))}
              >
                RETRY
              </button>
            )}
            {!confirmEject ? (
              <button
                className="key sm danger"
                disabled={busy}
                onClick={() => setConfirmEject(true)}
              >
                EJECT
              </button>
            ) : (
              <div className="eject-confirm">
                <label>
                  <input
                    type="checkbox"
                    checked={wipeFiles}
                    onChange={(e) => setWipeFiles(e.target.checked)}
                  />
                  delete downloaded files
                </label>
                <label>
                  <input
                    type="checkbox"
                    checked={wipeProvider}
                    onChange={(e) => setWipeProvider(e.target.checked)}
                  />
                  remove at provider
                </label>
                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    className="key sm danger"
                    disabled={busy}
                    onClick={() =>
                      run(
                        api.torrents.remove(t.id, {
                          files: wipeFiles,
                          provider: wipeProvider,
                        }),
                      )
                    }
                  >
                    CONFIRM EJECT
                  </button>
                  <button
                    className="key sm"
                    disabled={busy}
                    onClick={() => setConfirmEject(false)}
                  >
                    CANCEL
                  </button>
                </div>
              </div>
            )}
          </div>
          {actErr && (
            <div
              style={{ color: '#c23a1c', fontSize: 11.5, marginTop: 6 }}
              role="alert"
            >
              {actErr}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

function Mod({ t }: { t: Torrent }) {
  const [open, setOpen] = useState(false)
  const led = ledFor(t)
  return (
    <div className={`mod${open ? ' open' : ''}`}>
      <button
        className="modhead"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
      >
        <div className={`led ${led.kind}`}>{led.label}</div>
        <div style={{ minWidth: 0 }}>
          <div className="name">{t.name}</div>
          <div className="meta">{statusLine(t)}</div>
        </div>
        <Segments t={t} />
        <div className="stats">
          <div className="s1">{formatBytes(t.size)}</div>
          <div className="s2">{t.status}</div>
        </div>
        <span className="chev" aria-hidden="true">
          ▶
        </span>
      </button>
      {open && <Bay t={t} />}
    </div>
  )
}

// refreshTick bumps on every SSE notification; the list re-fetches shortly
// after (debounced with a max wait, since engine polls emit bursts but a
// sustained stream must not starve the refresh).
export function Rack({ refreshTick }: { refreshTick: number }) {
  const [torrents, setTorrents] = useState<Array<Torrent> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [retryTick, setRetryTick] = useState(0)
  const lastFetch = useRef(0)

  useEffect(() => {
    const starved = Date.now() - lastFetch.current > 2000
    const timer = setTimeout(
      () => {
        lastFetch.current = Date.now()
        api.torrents
          .list()
          .then((list) => {
            setTorrents(list)
            setError(null)
          })
          .catch((e: unknown) => {
            // Keep the stale list rendered; retry on our own if events go quiet.
            setError(e instanceof Error ? e.message : String(e))
            setTimeout(() => setRetryTick((n) => n + 1), 5000)
          })
      },
      refreshTick === 0 || starved ? 0 : 300,
    )
    return () => clearTimeout(timer)
  }, [refreshTick, retryTick])

  const banner = error ? (
    <div className="lcd rack-empty" style={{ marginBottom: 10 }}>
      <div className="big" style={{ color: 'var(--lcd-red)', fontSize: 13 }}>
        LIST STALE — {error}
      </div>
    </div>
  ) : null

  if (torrents === null) return banner
  if (torrents.length === 0) {
    return (
      <>
        {banner}
        <div className="lcd rack-empty">
          <div className="lbl">Torrents</div>
          <div className="big" style={{ fontSize: 15 }}>
            RACK EMPTY — INSERT A MAGNET TO BEGIN
          </div>
        </div>
      </>
    )
  }
  return (
    <>
      {banner}
      <div className="rack">
        {torrents.map((t) => (
          <Mod key={t.id} t={t} />
        ))}
      </div>
    </>
  )
}
