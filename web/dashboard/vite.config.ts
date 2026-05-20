import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://127.0.0.1:3121',
      '/goim': 'http://127.0.0.1:3111',
      '/metrics': 'http://127.0.0.1:3111',
      '/sub': {
        target: 'ws://127.0.0.1:3102',
        ws: true,
      },
    },
  },
})
