/// <reference types="vitest" />
import {defineConfig} from 'vite'
import {svelte} from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
// The `test` block configures Vitest (v4) which reuses this vite config, so
// the Svelte plugin transforms `.svelte` files under test too.
export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte()
  ],
  resolve: {
    preserveSymlinks: true
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.test.ts'],
    setupFiles: ['src/lib/editor/test-setup.ts']
  }
})
