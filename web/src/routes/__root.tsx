import {
  HeadContent,
  Link,
  Outlet,
  Scripts,
  createRootRoute,
} from '@tanstack/react-router'

import { AuthGate, useAuth } from '#/lib/auth'
import appCss from '../styles.css?url'

export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: 'utf-8' },
      { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      { title: 'debrid' },
    ],
    links: [
      { rel: 'stylesheet', href: appCss },
      { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' },
    ],
  }),
  shellComponent: RootDocument,
  component: Chassis,
})

function RootDocument({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body>
        {children}
        <Scripts />
      </body>
    </html>
  )
}

// SessionBadge shows who is signed in and offers logout; only rendered
// inside the gate, where useAuth is available.
function SessionBadge() {
  const { username, logout } = useAuth()
  return (
    <span className="session-badge">
      {username && <span className="session-user">{username}</span>}
      <button className="key sm" onClick={logout}>
        LOG OUT
      </button>
    </span>
  )
}

function Chassis() {
  return (
    <div className="device">
      <header className="device-top">
        <span className="screw" />
        <div className="brand">
          <span className="dot" />
          DEBRID<span style={{ color: 'var(--org)' }}>.</span>
        </div>
        <span className="model-label">SELF-HOSTED</span>
        <span className="flex-spacer" />
        <span className="screw" />
      </header>
      <AuthGate>
        <nav className="tabs">
          <Link
            to="/"
            className="tab"
            activeProps={{ className: 'tab on' }}
            activeOptions={{ exact: true }}
          >
            TORRENTS
          </Link>
          <Link
            to="/accounts"
            className="tab"
            activeProps={{ className: 'tab on' }}
          >
            ACCOUNTS
          </Link>
          <Link
            to="/settings"
            className="tab"
            activeProps={{ className: 'tab on' }}
          >
            SETTINGS
          </Link>
          <span className="flex-spacer" />
          <SessionBadge />
        </nav>
        <Outlet />
      </AuthGate>
      <footer className="strip">
        <span>API ● MCP ● SSE</span>
        <div className="grill">
          <i />
          <i />
          <i />
          <i />
          <i />
          <i />
        </div>
        <span>debrid-client</span>
      </footer>
    </div>
  )
}
