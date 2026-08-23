import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import {
  ApiError,
  PROVIDER_KINDS,
  PROVIDER_LABELS,
  api,
  openEvents,
} from '#/lib/api'
import { formatWhen } from '#/lib/format'
import type { Account, ProviderKind, User } from '#/lib/api'

export const Route = createFileRoute('/accounts')({ component: AccountsPage })

function StatusBadge({ a }: { a: Account }) {
  if (!a.enabled) return <span className="badge off">DISABLED</span>
  if (!a.user) return null
  if (!a.user.premium) return <span className="badge warn">FREE</span>
  return <span className="badge">PREMIUM</span>
}

function AccountCard({ a, onChanged }: { a: Account; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [force, setForce] = useState(false)

  const run = (p: Promise<unknown>, okText?: string, latch = false) => {
    setBusy(true)
    setMsg(null)
    p.then(
      () => {
        if (!latch) setBusy(false)
        if (okText) setMsg({ ok: true, text: okText })
        onChanged()
      },
      (e: unknown) => {
        setBusy(false)
        setMsg({
          ok: false,
          text: e instanceof Error ? e.message : String(e),
        })
      },
    )
  }

  return (
    <div className="acard">
      <h3>
        {a.name}
        <StatusBadge a={a} />
        {a.is_default && <span className="badge off">DEFAULT</span>}
      </h3>
      <div className="sub">
        {PROVIDER_LABELS[a.kind]}
        {a.user?.username ? ` · ${a.user.username}` : ''}
        {a.user?.plan ? ` · ${a.user.plan}` : ''}
        {a.user?.expires_at
          ? ` · expires ${formatWhen(a.user.expires_at)}`
          : ''}
      </div>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button
          className="key sm"
          disabled={busy}
          onClick={() =>
            run(
              api.accounts.test(a.id).then((u: User) => {
                setMsg({
                  ok: true,
                  text: `OK — ${u.premium ? 'premium' : 'free'}${u.username ? `, ${u.username}` : ''}`,
                })
              }),
            )
          }
        >
          TEST
        </button>
        <button
          className="key sm"
          disabled={busy}
          onClick={() =>
            run(
              api.accounts.update(a.id, { enabled: !a.enabled }),
              a.enabled ? 'disabled' : 'enabled',
            )
          }
        >
          {a.enabled ? 'DISABLE' : 'ENABLE'}
        </button>
        {!a.is_default && (
          <button
            className="key sm"
            disabled={busy}
            onClick={() =>
              run(
                api.accounts.update(a.id, { set_default: true }),
                'default set',
              )
            }
          >
            SET DEFAULT
          </button>
        )}
        {!confirmDelete ? (
          <button
            className="key sm danger"
            disabled={busy}
            onClick={() => setConfirmDelete(true)}
          >
            REMOVE
          </button>
        ) : (
          <div className="eject-confirm">
            <label>
              <input
                type="checkbox"
                checked={force}
                onChange={(e) => setForce(e.target.checked)}
              />
              also delete this account's torrents (locally)
            </label>
            <div style={{ display: 'flex', gap: 8 }}>
              <button
                className="key sm danger"
                disabled={busy}
                onClick={() =>
                  run(api.accounts.remove(a.id, { force }), undefined, true)
                }
              >
                CONFIRM REMOVE
              </button>
              <button
                className="key sm"
                disabled={busy}
                onClick={() => setConfirmDelete(false)}
              >
                CANCEL
              </button>
            </div>
          </div>
        )}
      </div>
      {msg && (
        <div className={msg.ok ? 'form-ok' : 'form-err'} role="alert">
          {msg.text}
        </div>
      )}
    </div>
  )
}

function AddAccountCard({ onChanged }: { onChanged: () => void }) {
  const [kind, setKind] = useState<ProviderKind>('torbox')
  const [name, setName] = useState('')
  const [key, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const add = () => {
    if (busy || key.trim() === '') return
    setBusy(true)
    setErr(null)
    api.accounts
      .add({
        kind,
        credentials: { api_key: key.trim() },
        ...(name.trim() ? { name: name.trim() } : {}),
      })
      .then(() => {
        setKey('')
        setName('')
        onChanged()
      })
      .catch((e: unknown) => setErr(e instanceof Error ? e.message : String(e)))
      .finally(() => setBusy(false))
  }

  return (
    <div className="acard">
      <h3>Add account</h3>
      <div className="sub">
        Credentials are verified against the provider before saving.
      </div>
      <div className="field">
        <label htmlFor="acc-kind">Provider</label>
        <select
          id="acc-kind"
          value={kind}
          onChange={(e) => setKind(e.target.value as ProviderKind)}
          disabled={busy}
        >
          {PROVIDER_KINDS.map((k) => (
            <option key={k} value={k}>
              {PROVIDER_LABELS[k]}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label htmlFor="acc-name">Name (optional)</label>
        <div className="well">
          <input
            id="acc-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={PROVIDER_LABELS[kind]}
            disabled={busy}
          />
        </div>
      </div>
      <div className="field">
        <label htmlFor="acc-key">API key</label>
        <div className="well">
          <input
            id="acc-key"
            type="password"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="paste the provider API key"
            disabled={busy}
          />
        </div>
      </div>
      <button
        className="key org"
        disabled={busy || key.trim() === ''}
        onClick={add}
      >
        {busy ? 'VERIFYING…' : 'CONNECT ACCOUNT'}
      </button>
      {err && (
        <div className="form-err" role="alert">
          {err}
        </div>
      )}
    </div>
  )
}

function AccountsPage() {
  const [accounts, setAccounts] = useState<Array<Account> | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [tick, setTick] = useState(0)

  useEffect(() => {
    api.accounts
      .list()
      .then((list) => {
        setAccounts(list)
        setError(null)
      })
      .catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 401) {
          setError('not authorized — connect on the TORRENTS tab first')
        } else {
          setError(e instanceof Error ? e.message : String(e))
        }
      })
  }, [tick])

  // Follow account.changed events (e.g. changes made via CLI/MCP).
  useEffect(() => {
    if (accounts === null) return
    return openEvents((type) => {
      if (type === 'account.changed') setTick((n) => n + 1)
    })
  }, [accounts === null])

  const refresh = () => setTick((n) => n + 1)

  if (error) {
    return (
      <div className="lcd rack-empty">
        <div className="big" style={{ color: 'var(--lcd-red)', fontSize: 14 }}>
          {error.toUpperCase()}
        </div>
      </div>
    )
  }
  if (accounts === null) return null

  return (
    <div className="cardrow">
      {accounts.map((a) => (
        <AccountCard key={a.id} a={a} onChanged={refresh} />
      ))}
      <AddAccountCard onChanged={refresh} />
    </div>
  )
}
