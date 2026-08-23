import { useEffect, useRef, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { ApiError, api, openEvents, setApiKey } from '#/lib/api'
import { InsertWell } from '#/components/insert'
import { Rack } from '#/components/rack'
import type { Status } from '#/lib/api'

export const Route = createFileRoute('/')({ component: Home })

function Home() {
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [needsKey, setNeedsKey] = useState(false)
  const [rejected, setRejected] = useState(false)
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
        if (e instanceof ApiError && e.status === 401) {
          // A rejected key on a repeat attempt deserves feedback, not silence.
          setRejected(needsKey && keyInput.trim() !== '')
          setNeedsKey(true)
        } else {
          setError(e instanceof Error ? e.message : String(e))
        }
      })
  }
  useEffect(load, [])

  // One SSE connection per page; every notification bumps the tick, which
  // re-fetches the status LCDs here and the torrent list in <Rack>. Keyed on
  // needsKey so a rotated key closes the stream and reconnects with the new
  // one once the user re-authorizes.
  const [tick, setTick] = useState(0)
  const authed = status !== null
  useEffect(() => {
    if (!authed || needsKey) return
    let disposed = false
    let close: (() => void) | undefined
    let retryTimer: ReturnType<typeof setTimeout>
    const connect = () => {
      close = openEvents(
        (type) => {
          if (type !== 'heartbeat') setTick((n) => n + 1)
        },
        () => {
          // Fatal close (e.g. key rejected): surface auth state via a normal
          // load, then retry the stream — a 401 flips needsKey and stops us.
          close?.()
          load()
          if (!disposed) retryTimer = setTimeout(connect, 5000)
        },
      )
    }
    connect()
    return () => {
      disposed = true
      clearTimeout(retryTimer)
      close?.()
    }
  }, [authed, needsKey])
  const lastLoad = useRef(0)
  useEffect(() => {
    if (tick === 0) return
    const starved = Date.now() - lastLoad.current > 2000
    const timer = setTimeout(
      () => {
        lastLoad.current = Date.now()
        load()
      },
      starved ? 0 : 300,
    )
    return () => clearTimeout(timer)
  }, [tick])

  if (needsKey) {
    return (
      <section style={{ maxWidth: 520, margin: '40px auto' }}>
        <div className="lcd" style={{ marginBottom: 14 }}>
          <div className="lbl">Authorization required</div>
          <div className="big" style={{ fontSize: 15 }}>
            {rejected
              ? 'KEY REJECTED — TRY AGAIN'
              : 'INSERT API KEY TO CONTINUE'}
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
      {authed && (
        <>
          <InsertWell onAdded={() => setTick((n) => n + 1)} />
          <Rack refreshTick={tick} />
        </>
      )}
    </section>
  )
}
