import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react'

import { ApiError, UNAUTHORIZED_EVENT, api } from '#/lib/api'
import { Login } from '#/components/login'
import { Onboarding } from '#/components/onboarding'
import type { AuthStatus } from '#/lib/api'

type AuthState = {
  username: string
  mode: string
  logout: () => void
}

const AuthContext = createContext<AuthState | null>(null)

// useAuth is only rendered beneath a resolved gate, so the context is set.
export function useAuth(): AuthState {
  const s = useContext(AuthContext)
  if (!s) throw new Error('useAuth outside AuthGate')
  return s
}

type Phase =
  | { kind: 'loading' }
  | { kind: 'error'; message: string }
  | { kind: 'onboarding'; status: AuthStatus }
  | { kind: 'login'; status: AuthStatus }
  | { kind: 'ready'; username: string; mode: string }

// AuthGate resolves the authentication state before rendering the app:
// onboarding when the server is unconfigured, the login screen when the
// session is missing or expired, the app otherwise.
export function AuthGate({ children }: { children: React.ReactNode }) {
  const [phase, setPhase] = useState<Phase>({ kind: 'loading' })

  const resolve = useCallback(() => {
    api.auth
      .status()
      .then(async (status) => {
        if (!status.configured) {
          setPhase({ kind: 'onboarding', status })
          return
        }
        try {
          const me = await api.auth.me()
          setPhase({
            kind: 'ready',
            username: me.username ?? '',
            mode: me.mode,
          })
        } catch (e) {
          if (e instanceof ApiError && e.status === 401) {
            setPhase({ kind: 'login', status })
          } else {
            throw e
          }
        }
      })
      .catch((e: unknown) =>
        setPhase({
          kind: 'error',
          message: e instanceof Error ? e.message : String(e),
        }),
      )
  }, [])

  useEffect(resolve, [resolve])

  // Any 401 from the app flips back to the login screen.
  useEffect(() => {
    const onUnauthorized = () =>
      setPhase((p) => (p.kind === 'ready' ? { kind: 'loading' } : p))
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [])
  useEffect(() => {
    if (phase.kind === 'loading') resolve()
  }, [phase.kind, resolve])

  if (phase.kind === 'loading') return null
  if (phase.kind === 'error') {
    return (
      <div className="lcd rack-empty">
        <div className="big" style={{ color: 'var(--lcd-red)', fontSize: 14 }}>
          SERVER UNREACHABLE — {phase.message.toUpperCase()}
        </div>
      </div>
    )
  }
  if (phase.kind === 'onboarding') {
    return <Onboarding onDone={resolve} />
  }
  if (phase.kind === 'login') {
    return <Login status={phase.status} onDone={resolve} />
  }
  return (
    <AuthContext.Provider
      value={{
        username: phase.username,
        mode: phase.mode,
        logout: () => {
          api.auth.logout().finally(resolve)
        },
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

// authErrorFromURL pulls ?auth_error= (set by the OIDC callback redirect) and
// strips it from the address bar. Rendered strictly as text by callers.
export function authErrorFromURL(): string | null {
  const params = new URLSearchParams(window.location.search)
  const err = params.get('auth_error')
  if (err !== null) {
    params.delete('auth_error')
    const rest = params.toString()
    window.history.replaceState(
      null,
      '',
      window.location.pathname + (rest ? `?${rest}` : ''),
    )
  }
  return err
}
