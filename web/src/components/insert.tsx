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
  const addFiles = (list: FileList | null | undefined) => {
    if (!list || list.length === 0 || busy) return
    run(Promise.all([...list].map((f) => api.torrents.addFile(f))))
  }

  return (
    <div style={{ marginBottom: 18 }}>
      <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap' }}>
        <div
          className={`well${drag ? ' drag' : ''}`}
          style={{ flex: 1 }}
          onDragOver={(e) => {
            // Only advertise a drop target for actual files, not text drags.
            if (!e.dataTransfer.types.includes('Files')) return
            e.preventDefault()
            setDrag(true)
          }}
          onDragLeave={() => setDrag(false)}
          onDrop={(e) => {
            e.preventDefault()
            setDrag(false)
            addFiles(e.dataTransfer.files)
          }}
        >
          <input
            placeholder="PASTE MAGNET LINK — OR DROP A .TORRENT FILE HERE"
            value={magnet}
            onChange={(e) => setMagnet(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.nativeEvent.isComposing) addMagnet()
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
          multiple
          accept=".torrent,application/x-bittorrent"
          style={{ display: 'none' }}
          onChange={(e) => {
            addFiles(e.target.files)
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
