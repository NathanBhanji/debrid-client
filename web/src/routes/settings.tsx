import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/settings')({
  component: () => <p style={{ color: 'var(--mut)' }}>Settings — next PR.</p>,
})
