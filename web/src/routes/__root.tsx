import {
  HeadContent,
  Link,
  Outlet,
  Scripts,
  createRootRoute,
} from '@tanstack/react-router'

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
      </nav>
      <Outlet />
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
