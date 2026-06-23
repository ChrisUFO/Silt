import {defineConfig} from 'vite'
import {svelte} from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
// Vitest config lives in vitest.config.ts (which takes precedence over a
// test key here). See vitest.config.ts for the test environment, setup
// files, and the svelteTesting plugin.
export default defineConfig({
  plugins: [
    tailwindcss(),
    svelte()
  ],
  resolve: {
    preserveSymlinks: true
  }
})
