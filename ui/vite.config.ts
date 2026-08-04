import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Where the dev server proxies /api to. Not VITE_-prefixed: this is read by
// Vite itself (Node, at startup), not bundled into client code - the client
// just calls same-origin /api and never needs to know this address, which
// is what keeps the UI CORS-free and .env-free by default. Override for a
// broker running on a different host/port.
const BROKER_URL = process.env.BROKER_URL ?? 'http://localhost:8878'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    proxy: {
      '/api': {
        target: BROKER_URL,
        changeOrigin: true,
      },
    },
  },
})
