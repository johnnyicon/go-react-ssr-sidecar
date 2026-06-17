import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

/**
 * SSR bundle build configuration.
 * Outputs to: frontend/dist-ssr/entry-server.js
 * Copied into the Docker image alongside ssr-server.mjs
 *
 * Key settings:
 *   build.ssr — tells Vite to use the entry point as an SSR module (preserves
 *               named exports like `render`)
 *   ssr.noExternal: true — bundles ALL npm dependencies into entry-server.js so
 *               the production container only needs Node.js itself (no node_modules)
 *   format: 'es' — ES module output so ssr-server.mjs can `import` it
 *
 * Running `npm run build:ssr` produces a single self-contained bundle that the
 * Go binary copies next to itself in the Docker image.
 */
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    outDir: path.resolve(__dirname, 'dist-ssr'),
    emptyOutDir: true,
    // build.ssr correctly preserves named exports from the entry point.
    // Without this, Rollup treats it as a library and may tree-shake the export.
    ssr: path.resolve(__dirname, 'src/entry-server.tsx'),
    rollupOptions: {
      output: {
        format: 'es',
      },
    },
  },
  ssr: {
    // Bundle everything — no node_modules needed in the production Docker image.
    // Only Node.js itself + dist-ssr/entry-server.js + ssr-server.mjs.
    noExternal: true,
    target: 'node',
  },
})
