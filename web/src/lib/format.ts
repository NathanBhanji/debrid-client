const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '0 B'
  let i = 0
  let v = n
  // 999.5+ would round up to a displayed "1000", so bump the unit there.
  while (v >= 999.5 && i < UNITS.length - 1) {
    v /= 1000
    i++
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(1)} ${UNITS[i]}`
}

export function formatSpeed(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`
}

export function formatWhen(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    day: '2-digit',
    month: 'short',
    ...(d.getFullYear() !== new Date().getFullYear()
      ? { year: 'numeric' }
      : {}),
    hour: '2-digit',
    minute: '2-digit',
  })
}
