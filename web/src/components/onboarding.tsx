import { useState } from 'react'

import { api } from '#/lib/api'
import { authErrorFromURL } from '#/lib/auth'

type Method = 'password' | 'oidc'

// Onboarding runs once per server: pick an auth method and prove ownership
// with the API key that `debrid serve` printed. Password setup signs in
// immediately; OIDC hands the browser to the provider to pin the identity.
export function Onboarding({ onDone }: { onDone: () => void }) {
  const [method, setMethod] = useState<Method>('password')
  const [apiKey, setApiKey] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [issuer, setIssuer] = useState('')
  const [clientID, setClientID] = useState('')
  const [clientSecret, setClientSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(() => authErrorFromURL())

  const run = (p: Promise<unknown>) => {
    setBusy(true)
    setError(null)
    p.catch((e: unknown) => {
      setError(e instanceof Error ? e.message : String(e))
      setBusy(false)
    })
  }

  const submitPassword = () => {
    if (password !== confirm) {
      setError('passwords do not match')
      return
    }
    run(
      api.auth
        .setupPassword(apiKey.trim(), username.trim(), password)
        .then(onDone),
    )
  }

  const submitOIDC = () => {
    run(
      api.auth
        .setupOIDC(
          apiKey.trim(),
          issuer.trim(),
          clientID.trim(),
          clientSecret.trim(),
        )
        .then(({ auth_url }) => {
          window.location.assign(auth_url)
        }),
    )
  }

  const canSubmit =
    apiKey.trim() !== '' &&
    (method === 'password'
      ? username.trim() !== '' && password !== '' && confirm !== ''
      : issuer.trim() !== '' && clientID.trim() !== '')

  return (
    <section style={{ maxWidth: 560, margin: '30px auto' }}>
      <div className="lcd" style={{ marginBottom: 14 }}>
        <div className="lbl">First run</div>
        <div className="big" style={{ fontSize: 15 }}>
          CHOOSE HOW TO SIGN IN TO THIS SERVER
        </div>
      </div>

      <div className="tabs" style={{ marginBottom: 14 }}>
        <button
          className={`tab${method === 'password' ? ' on' : ''}`}
          onClick={() => setMethod('password')}
        >
          USERNAME &amp; PASSWORD
        </button>
        <button
          className={`tab${method === 'oidc' ? ' on' : ''}`}
          onClick={() => setMethod('oidc')}
        >
          SSO / PASSKEYS (OIDC)
        </button>
      </div>

      <div className="acard">
        {method === 'password' ? (
          <>
            <div className="field">
              <label htmlFor="ob-user">Username</label>
              <div className="well">
                <input
                  id="ob-user"
                  value={username}
                  autoComplete="username"
                  onChange={(e) => setUsername(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
            <div className="field">
              <label htmlFor="ob-pass">Password (min 10 characters)</label>
              <div className="well">
                <input
                  id="ob-pass"
                  type="password"
                  value={password}
                  autoComplete="new-password"
                  onChange={(e) => setPassword(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
            <div className="field">
              <label htmlFor="ob-confirm">Confirm password</label>
              <div className="well">
                <input
                  id="ob-confirm"
                  type="password"
                  value={confirm}
                  autoComplete="new-password"
                  onChange={(e) => setConfirm(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
          </>
        ) : (
          <>
            <div className="sub" style={{ marginBottom: 12 }}>
              Works with any OIDC provider — e.g. a self-hosted Pocket ID for
              passkey sign-in. Register a client there first with callback{' '}
              <code>{window.location.origin}/api/v1/auth/oidc/callback</code>,
              then finish here.
            </div>
            <div className="field">
              <label htmlFor="ob-issuer">Issuer URL</label>
              <div className="well">
                <input
                  id="ob-issuer"
                  placeholder="https://id.example.com"
                  value={issuer}
                  onChange={(e) => setIssuer(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
            <div className="field">
              <label htmlFor="ob-client">Client ID</label>
              <div className="well">
                <input
                  id="ob-client"
                  value={clientID}
                  onChange={(e) => setClientID(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
            <div className="field">
              <label htmlFor="ob-secret">Client secret</label>
              <div className="well">
                <input
                  id="ob-secret"
                  type="password"
                  value={clientSecret}
                  onChange={(e) => setClientSecret(e.target.value)}
                  disabled={busy}
                />
              </div>
            </div>
          </>
        )}

        <div className="field" style={{ marginTop: 16 }}>
          <label htmlFor="ob-key">
            Server API key — printed by `debrid serve`; proves you own this
            server
          </label>
          <div className="well">
            <input
              id="ob-key"
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              disabled={busy}
            />
          </div>
        </div>

        <button
          className="key org"
          disabled={busy || !canSubmit}
          onClick={method === 'password' ? submitPassword : submitOIDC}
        >
          {busy
            ? 'WORKING…'
            : method === 'password'
              ? 'CREATE ACCOUNT'
              : 'CONTINUE AT PROVIDER'}
        </button>
        {error && (
          <div className="form-err" role="alert">
            {error}
          </div>
        )}
      </div>
    </section>
  )
}
