import { useEffect, useState } from 'react'

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
  }
}

function progressFor(t: Torrent): number {
  if (t.status === 'completed') return 1
  if (t.status === 'finished') return t.local_progress
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
  }
}

function Segments({ t }: { t: Torrent }) {
  const { segClass } = ledFor(t)
  const p = progressFor(t)
  const lit = Math.round(p * SEGMENTS)
  const active = t.status !== 'completed' && t.status !== 'error'
  return (
    <div className="seg">
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

function Bay({ t }: { t: Torrent }) {
  const downloads = t.downloads
  const files = t.files
  return (
    <div className="bay">
      <div className="cols">
        <div className="filelist">
          <div className="hd">
            {downloads.length > 0 ? 'LOCAL DOWNLOADS' : 'FILES AT PROVIDER'}
          </div>
          {downloads.length > 0
            ? downloads.map((d) => (
                <div className="frow" key={d.id}>
                  <span className="fp">{d.path}</span>
                  <span className="fs">{formatBytes(d.size)}</span>
                  <span className={`fst ${downloadStateClass(d.state)}`}>
                    {d.state === 'downloading'
                      ? `${Math.floor(d.progress * 100)}%`
                      : d.state}
                  </span>
                </div>
              ))
            : files.map((f) => (
                <div className="frow" key={f.id}>
                  <span className="fp">{f.path}</span>
                  <span className="fs">{formatBytes(f.size)}</span>
                  <span className={`fst ${f.selected ? 'ok' : 'wait'}`}>
                    {f.selected ? 'sel' : '—'}
                  </span>
                </div>
              ))}
          {downloads.length === 0 && files.length === 0 && (
            <div className="frow">
              <span className="fp" style={{ color: 'var(--lcd-dim)' }}>
                no file listing yet
              </span>
            </div>
          )}
        </div>
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
      <button className="modhead" onClick={() => setOpen((o) => !o)}>
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
        <span className="chev">▶</span>
      </button>
      {open && <Bay t={t} />}
    </div>
  )
}

// refreshTick bumps on every SSE notification; the list re-fetches shortly
// after (debounced, since engine polls emit bursts).
export function Rack({ refreshTick }: { refreshTick: number }) {
  const [torrents, setTorrents] = useState<Array<Torrent> | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const timer = setTimeout(
      () => {
        api.torrents
          .list()
          .then((list) => {
            setTorrents(list)
            setError(null)
          })
          .catch((e: unknown) =>
            setError(e instanceof Error ? e.message : String(e)),
          )
      },
      refreshTick === 0 ? 0 : 300,
    )
    return () => clearTimeout(timer)
  }, [refreshTick])

  if (error) {
    return (
      <div className="lcd rack-empty">
        <div className="lbl">Torrents</div>
        <div className="big" style={{ color: 'var(--lcd-red)', fontSize: 15 }}>
          LIST UNAVAILABLE — {error}
        </div>
      </div>
    )
  }
  if (torrents === null) return null
  if (torrents.length === 0) {
    return (
      <div className="lcd rack-empty">
        <div className="lbl">Torrents</div>
        <div className="big" style={{ fontSize: 15 }}>
          RACK EMPTY — INSERT A MAGNET TO BEGIN
        </div>
      </div>
    )
  }
  return (
    <div className="rack">
      {torrents.map((t) => (
        <Mod key={t.id} t={t} />
      ))}
    </div>
  )
}
