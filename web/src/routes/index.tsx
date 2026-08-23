import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { ApiError, api, getApiKey, setApiKey } from '#/lib/api'
import type { Status } from '#/lib/api'

export const Route = createFileRoute('/')({ component: Home })

function Home() {
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [needsKey, setNeedsKey] = useState(false)
  const [keyInput, setKeyInput] = useState('')

  const load = () => {
    api
      .status()
      .then((s) => {
        setStatus(s)
        setError(null)
        setNeedsKey(false)
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) setNeedsKey(true)
        else setError(e instanceof Error ? e.message : String(e))
      })
  }
  useEffect(load, [])

  if (needsKey) {
    return (
      <section style={{ maxWidth: 520, margin: '40px auto' }}>
        <div className="lcd" style={{ marginBottom: 14 }}>
          <div className="lbl">Authorization required</div>
          <div className="big" style={{ fontSize: 15 }}>
            INSERT API KEY TO CONTINUE
          </div>
        </div>
        <div style={{ display: 'flex', gap: 10 }}>
          <div className="well" style={{ flex: 1 }}>
            <input
              placeholder="API KEY — printed by `debrid serve` on first start"
              value={keyInput}
              onChange={(e) => setKeyInput(e.target.value)}
              type="password"
            />
          </div>
          <button
            className="key org"
            onClick={() => {
              setApiKey(keyInput.trim())
              load()
            }}
          >
            CONNECT
          </button>
        </div>
      </section>
    )
  }

  return (
    <section>
      <div className="lcd-row">
        <div className="lcd wide">
          <div className="lbl">System</div>
          <div className="big">
            {error
              ? `OFFLINE — ${error}`
              : status
                ? `READY · ${status.version}`
                : 'CONNECTING…'}
          </div>
        </div>
        <div className="lcd">
          <div className="lbl">Torrents</div>
          <div className="big">
            {status
              ? Object.values(status.torrents).reduce((a, b) => a + b, 0)
              : '—'}
          </div>
        </div>
        <div className="lcd">
          <div className="lbl">Accounts</div>
          <div className="big">{status?.accounts ?? '—'}</div>
        </div>
        <div className="lcd">
          <div className="lbl">Disk free</div>
          <div className="big">
            {status ? Math.round(status.disk_free_bytes / 1e9) : '—'}
            <span className="u"> GB</span>
          </div>
        </div>
      </div>
      <p style={{ color: 'var(--mut)', fontSize: 12 }}>
        Torrent rack lands in the next PR — this scaffold proves the embedded
        SPA, design tokens, typed API client and auth flow.
        {getApiKey() ? '' : ' No API key set yet.'}
      </p>
    </section>
  )
}
