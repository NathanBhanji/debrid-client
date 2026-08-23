import { defineConfig } from 'vite'

import { tanstackStart } from '@tanstack/react-start/plugin/vite'

import viteReact from '@vitejs/plugin-react'

// The UI ships as a static SPA embedded in the Go binary (internal/webui):
// SPA mode prerenders an index shell and emits client assets only — no Node
// server at runtime. The Go server serves /api, /mcp and these static files.
const config = defineConfig({
  resolve: { tsconfigPaths: true },
  plugins: [tanstackStart({ spa: { enabled: true } }), viteReact()],
  server: {
    port: 3000,
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/openapi.json': 'http://127.0.0.1:8080',
    },
  },
})

export default config
