import { useRef, useState } from 'react'

import { api } from '#/lib/api'

// Magnet paste + .torrent drop/browse. Adds go through the API; the caller
// bumps its refresh tick so the rack picks the new torrent up immediately
// (SSE will confirm it anyway).
export function InsertWell({ onAdded }: { onAdded: () => void }) {
  const [magnet, setMagnet] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [drag, setDrag] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const run = (p: Promise<unknown>) => {
    setBusy(true)
    setError(null)
    p.then(() => {
      setMagnet('')
      onAdded()
    })
      .catch((e: unknown) =>
        setError(e instanceof Error ? e.message : String(e)),
      )
      .finally(() => setBusy(false))
  }

  const addMagnet = () => {
    const m = magnet.trim()
    if (!m || busy) return
    run(api.torrents.addMagnet(m))
  }
  const addFile = (file: File | undefined) => {
    if (!file || busy) return
    run(api.torrents.addFile(file))
  }

  return (
    <div style={{ marginBottom: 18 }}>
      <div style={{ display: 'flex', gap: 10 }}>
        <div
          className={`well${drag ? ' drag' : ''}`}
          style={{ flex: 1 }}
          onDragOver={(e) => {
            e.preventDefault()
            setDrag(true)
          }}
          onDragLeave={() => setDrag(false)}
          onDrop={(e) => {
            e.preventDefault()
            setDrag(false)
            addFile(e.dataTransfer.files[0])
          }}
        >
          <input
            placeholder="PASTE MAGNET LINK — OR DROP A .TORRENT FILE HERE"
            value={magnet}
            onChange={(e) => setMagnet(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') addMagnet()
            }}
            disabled={busy}
          />
        </div>
        <button
          className="key sm"
          onClick={() => fileInput.current?.click()}
          disabled={busy}
        >
          FILE…
        </button>
        <button
          className="key org"
          onClick={addMagnet}
          disabled={busy || magnet.trim() === ''}
        >
          {busy ? 'LOADING…' : 'LOAD'}
        </button>
        <input
          ref={fileInput}
          type="file"
          accept=".torrent,application/x-bittorrent"
          style={{ display: 'none' }}
          onChange={(e) => {
            addFile(e.target.files?.[0])
            e.target.value = ''
          }}
        />
      </div>
      {error && (
        <div
          style={{ color: '#c23a1c', fontSize: 12, margin: '8px 2px 0' }}
          role="alert"
        >
          {error}
        </div>
      )}
    </div>
  )
}
