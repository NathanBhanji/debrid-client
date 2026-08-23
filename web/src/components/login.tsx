import { useState } from 'react'

import { api } from '#/lib/api'
import { authErrorFromURL } from '#/lib/auth'
import type { AuthStatus } from '#/lib/api'

// Login renders the sign-in screen for the configured mode: a password form
// or a hand-off to the OIDC provider.
export function Login({
  status,
  onDone,
}: {
  status: AuthStatus
  onDone: () => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(() => authErrorFromURL())

  const submit = () => {
    if (busy || username === '' || password === '') return
    setBusy(true)
    setError(null)
    api.auth
      .login(username, password)
      .then(onDone)
      .catch((e: unknown) => {
        setError(e instanceof Error ? e.message : String(e))
        setBusy(false)
      })
  }

  const issuerHost = (() => {
    try {
      return new URL(status.oidc_issuer ?? '').host
    } catch {
      return status.oidc_issuer ?? ''
    }
  })()

  return (
    <section style={{ maxWidth: 460, margin: '40px auto' }}>
      <div className="lcd" style={{ marginBottom: 14 }}>
        <div className="lbl">Authorization required</div>
        <div className="big" style={{ fontSize: 15 }}>
          {status.mode === 'oidc' ? 'SIGN IN TO CONTINUE' : 'ENTER CREDENTIALS'}
        </div>
      </div>
      {status.mode === 'oidc' ? (
        <a className="key org first-run-key" href="/api/v1/auth/oidc/start">
          SIGN IN WITH {issuerHost.toUpperCase() || 'SSO'}
        </a>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div className="well">
            <input
              placeholder="USERNAME"
              value={username}
              autoComplete="username"
              onChange={(e) => setUsername(e.target.value)}
              disabled={busy}
            />
          </div>
          <div className="well">
            <input
              placeholder="PASSWORD"
              type="password"
              value={password}
              autoComplete="current-password"
              onChange={(e) => setPassword(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.nativeEvent.isComposing) submit()
              }}
              disabled={busy}
            />
          </div>
          <div>
            <button
              className="key org"
              onClick={submit}
              disabled={busy || username === '' || password === ''}
            >
              {busy ? 'SIGNING IN…' : 'SIGN IN'}
            </button>
          </div>
        </div>
      )}
      {error && (
        <div className="form-err" role="alert">
          {error}
        </div>
      )}
    </section>
  )
}
