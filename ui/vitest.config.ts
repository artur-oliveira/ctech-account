import { defineConfig } from 'vitest/config'
import path from 'node:path'

export default defineConfig({
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    globals: true,
    // @aoctech/ui ships ESM with extensionless relative imports, which Node refuses; inline lets Vite resolve them.
    server: { deps: { inline: ['@aoctech/ui'] } },
  },
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
})
