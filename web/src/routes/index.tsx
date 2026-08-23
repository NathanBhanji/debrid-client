import { useEffect, useRef, useState } from 'react'
import { Link, createFileRoute } from '@tanstack/react-router'

import { api, openEvents } from '#/lib/api'
import { InsertWell } from '#/components/insert'
import { Rack } from '#/components/rack'
import type { Status } from '#/lib/api'

export const Route = createFileRoute('/')({ component: Home })

function Home() {
  const [status, setStatus] = useState<Status | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    api
      .status()
      .then((s) => {
        setStatus(s)
        setError(null)
      })
      .catch((e: unknown) =>
        setError(e instanceof Error ? e.message : String(e)),
      )
  }
  useEffect(load, [])

  // One SSE connection per page; every notification bumps the tick, which
  // re-fetches the status LCDs here and the torrent list in <Rack>. The auth
  // gate unmounts this page when the session dies, closing the stream.
  const [tick, setTick] = useState(0)
  const authed = status !== null
  useEffect(() => {
    if (!authed) return
    let disposed = false
    let close: (() => void) | undefined
    let retryTimer: ReturnType<typeof setTimeout>
    const connect = () => {
      close = openEvents(
        (type) => {
          if (type !== 'heartbeat') setTick((n) => n + 1)
        },
        () => {
          // Fatal close (e.g. revoked session): a refetch surfaces the 401,
          // which flips the auth gate to the login screen.
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
  }, [authed])
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
      {authed && status.accounts === 0 ? (
        <div className="lcd rack-empty">
          <div className="lbl">First run</div>
          <div className="big" style={{ fontSize: 15 }}>
            NO PROVIDER ACCOUNT CONNECTED
          </div>
          <div style={{ marginTop: 16 }}>
            <Link to="/accounts" className="key org first-run-key">
              CONNECT A PROVIDER
            </Link>
          </div>
        </div>
      ) : authed ? (
        <>
          <InsertWell onAdded={() => setTick((n) => n + 1)} />
          <Rack refreshTick={tick} />
        </>
      ) : null}
    </section>
  )
}
