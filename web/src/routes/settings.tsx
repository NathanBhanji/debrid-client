import { useEffect, useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { UNAUTHORIZED_EVENT, api } from '#/lib/api'
import { useAuth } from '#/lib/auth'
import type { Settings } from '#/lib/api'

export const Route = createFileRoute('/settings')({ component: SettingsPage })

function Toggle({
  label,
  on,
  onChange,
}: {
  label: string
  on: boolean
  onChange: (v: boolean) => void
}) {
  return (
    <div className="toggle">
      <span>{label}</span>
      <button
        type="button"
        className={`sw${on ? ' on' : ''}`}
        role="switch"
        aria-checked={on}
        aria-label={label}
        onClick={() => onChange(!on)}
      >
        <i />
      </button>
    </div>
  )
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  id,
  type,
}: {
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  id: string
  type?: string
}) {
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      <div className="well">
        <input
          id={id}
          value={value}
          type={type}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
        />
      </div>
    </div>
  )
}

// ChangePasswordCard is only offered in password mode; a successful change
// revokes every session (including this one), so the auth gate will show the
// login screen right after.
function ChangePasswordCard() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<string | null>(null)

  const submit = () => {
    if (next !== confirm) {
      setMsg('new passwords do not match')
      return
    }
    setBusy(true)
    setMsg(null)
    api.auth
      .changePassword(current, next)
      .then(() => {
        // Every session (including this one) is now revoked. Nothing on this
        // page issues another request, so flip the auth gate explicitly.
        window.dispatchEvent(new Event(UNAUTHORIZED_EVENT))
      })
      .catch((e: unknown) => {
        setMsg(e instanceof Error ? e.message : String(e))
        setBusy(false)
      })
  }

  return (
    <div className="acard">
      <h3>Change password</h3>
      <div className="sub">
        Saving signs you out everywhere; log back in with the new password.
      </div>
      <Field
        id="pw-current"
        label="Current password"
        value={current}
        onChange={setCurrent}
        type="password"
      />
      <Field
        id="pw-new"
        label="New password (min 10 characters)"
        value={next}
        onChange={setNext}
        type="password"
      />
      <Field
        id="pw-confirm"
        label="Confirm new password"
        value={confirm}
        onChange={setConfirm}
        type="password"
      />
      <button
        className="key org"
        disabled={busy || current === '' || next === '' || confirm === ''}
        onClick={submit}
      >
        {busy ? 'CHANGING…' : 'CHANGE PASSWORD'}
      </button>
      {msg && (
        <div className="form-err" role="alert">
          {msg}
        </div>
      )}
    </div>
  )
}

// The form edits string shadows of the numeric/list fields so partial input
// isn't fought by the parser; parsing happens on save.
function SettingsPage() {
  const { mode } = useAuth()
  const [settings, setSettings] = useState<Settings | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(
    null,
  )
  const [busy, setBusy] = useState(false)
  const [categories, setCategories] = useState('')
  const [retries, setRetries] = useState('')
  const [torrentRetries, setTorrentRetries] = useState('')
  const [minSize, setMinSize] = useState('')

  useEffect(() => {
    api.settings
      .get()
      .then((s) => {
        setSettings(s)
        setCategories((s.categories ?? []).join(', '))
        setRetries(String(s.torrent_defaults.download_retries ?? 0))
        setTorrentRetries(String(s.torrent_defaults.torrent_retries ?? 0))
        setMinSize(String(s.torrent_defaults.min_file_size ?? 0))
        setError(null)
      })
      .catch((e: unknown) =>
        setError(e instanceof Error ? e.message : String(e)),
      )
  }, [])

  if (error) {
    return (
      <div className="lcd rack-empty">
        <div className="big" style={{ color: 'var(--lcd-red)', fontSize: 14 }}>
          {error.toUpperCase()}
        </div>
      </div>
    )
  }
  if (settings === null) return null

  const d = settings.torrent_defaults
  const setDefaults = (patch: Partial<typeof d>) =>
    setSettings({ ...settings, torrent_defaults: { ...d, ...patch } })

  const save = () => {
    // Empty retry counts are ambiguous ('' coerces to 0 = retries off), so
    // reject them; an empty min size genuinely means no minimum.
    const nums = {
      download_retries: retries.trim() === '' ? NaN : Number(retries),
      torrent_retries:
        torrentRetries.trim() === '' ? NaN : Number(torrentRetries),
      min_file_size: Number(minSize),
    }
    for (const [k, v] of Object.entries(nums)) {
      if (!Number.isFinite(v) || v < 0 || !Number.isInteger(v)) {
        setSaveMsg({
          ok: false,
          text: `${k.replaceAll('_', ' ')}: not a non-negative integer`,
        })
        return
      }
    }
    const body: Settings = {
      ...settings,
      categories: categories
        .split(',')
        .map((c) => c.trim())
        .filter(Boolean),
      torrent_defaults: { ...d, ...nums },
    }
    setBusy(true)
    setSaveMsg(null)
    api.settings
      .update(body)
      .then((s) => {
        setSettings(s)
        setSaveMsg({ ok: true, text: 'saved' })
      })
      .catch((e: unknown) =>
        setSaveMsg({
          ok: false,
          text: e instanceof Error ? e.message : String(e),
        }),
      )
      .finally(() => setBusy(false))
  }

  return (
    <div className="cardrow">
      <div className="acard">
        <h3>Torrent defaults</h3>
        <div className="sub">
          Applied to new torrents; per-torrent settings override.
        </div>
        <Toggle
          label="Extract archives after download"
          on={d.unpack ?? false}
          onChange={(v) => setDefaults({ unpack: v })}
        />
        <Toggle
          label="Remove from provider when finished"
          on={d.finished_action === 'remove_from_provider'}
          onChange={(v) =>
            setDefaults({
              finished_action: v ? 'remove_from_provider' : 'keep',
            })
          }
        />
        <div style={{ height: 12 }} />
        <Field
          id="set-finished-delay"
          label="Delay before finished action"
          value={d.finished_delay ?? ''}
          onChange={(v) => setDefaults({ finished_delay: v })}
          placeholder="e.g. 10m — empty for none"
        />
        <Field
          id="set-lifetime"
          label="Provider lifetime"
          value={d.lifetime ?? ''}
          onChange={(v) => setDefaults({ lifetime: v })}
          placeholder="fail if not finished within, e.g. 72h — empty = never"
        />
        <Field
          id="set-delete-on-error"
          label="Delete on error after"
          value={d.delete_on_error ?? ''}
          onChange={(v) => setDefaults({ delete_on_error: v })}
          placeholder="e.g. 24h — empty = never"
        />
        <Field
          id="set-dl-retries"
          label="Download retries per file"
          value={retries}
          onChange={setRetries}
        />
        <Field
          id="set-torrent-retries"
          label="Torrent re-adds after provider error"
          value={torrentRetries}
          onChange={setTorrentRetries}
        />
      </div>
      <div className="acard">
        <h3>File filters &amp; library</h3>
        <div className="sub">Defaults for which files get downloaded.</div>
        <Field
          id="set-include"
          label="Include regex"
          value={d.include_regex ?? ''}
          onChange={(v) => setDefaults({ include_regex: v })}
          placeholder="only matching paths; overrides exclude"
        />
        <Field
          id="set-exclude"
          label="Exclude regex"
          value={d.exclude_regex ?? ''}
          onChange={(v) => setDefaults({ exclude_regex: v })}
          placeholder="skip matching paths"
        />
        <Field
          id="set-min-size"
          label="Minimum file size (bytes)"
          value={minSize}
          onChange={setMinSize}
        />
        <Field
          id="set-categories"
          label="Categories"
          value={categories}
          onChange={setCategories}
          placeholder="comma-separated, e.g. movies, tv"
        />
        <div style={{ marginTop: 16 }}>
          <button className="key org" disabled={busy} onClick={save}>
            {busy ? 'SAVING…' : 'SAVE SETTINGS'}
          </button>
        </div>
        {saveMsg && (
          <div className={saveMsg.ok ? 'form-ok' : 'form-err'} role="alert">
            {saveMsg.text}
          </div>
        )}
      </div>
      {mode === 'password' && <ChangePasswordCard />}
    </div>
  )
}
