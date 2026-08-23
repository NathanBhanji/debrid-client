import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/accounts')({
  component: () => <p style={{ color: 'var(--mut)' }}>Accounts — next PR.</p>,
})
