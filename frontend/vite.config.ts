import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

/**
 * Client build configuration.
 * Outputs to: frontend/dist/
 * Copied into: static/dist/ during Docker build (see Dockerfile.web stage 2)
 *
 * CUSTOMIZE:
 *   - Add @tailwindcss/vite if using Tailwind v4
 *   - Add @sentry/vite-plugin for source map uploads
 *   - Adjust manualChunks to split your vendor deps
 *   - Add proxy entries for your API routes in server.proxy
 */
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    // Proxy API calls to the Go API server in dev.
    // CUSTOMIZE: Add any other paths your API serves (e.g. /images, /sitemap.xml)
    proxy: {
      '/api': {
        target: process.env.API_URL || 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: path.resolve(__dirname, 'dist'),
    emptyOutDir: true,
    sourcemap: 'hidden',
    rollupOptions: {
      output: {
        // Split vendor chunks for better browser caching.
        // Vite adds content hashes so these are safe to cache forever.
        manualChunks(id) {
          if (
            id.includes('/node_modules/react/') ||
            id.includes('/node_modules/react-dom/') ||
            id.includes('/node_modules/scheduler/')
          ) {
            return 'vendor-react'
          }
          if (id.includes('/node_modules/@tanstack/')) {
            return 'vendor-router'
          }
        },
      },
    },
  },
})
