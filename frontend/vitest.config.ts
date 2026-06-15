import {defineConfig} from 'vitest/config'
import {svelte} from '@sveltejs/vite-plugin-svelte'
import {svelteTesting} from '@testing-library/svelte/vite'

// Vitest config for the theme engine coverage (#74) and the picker
// component tests (#50). The Svelte plugin compiles .svelte components
// (theme store + AppearanceTab). jsdom provides a DOM. The svelteTesting
// plugin sets the browser export condition so Svelte resolves to its
// client build (the default Node/server build throws
// "mount(...) is not available on the server" under vitest).
export default defineConfig({
  plugins: [svelte(), svelteTesting()],
  resolve: {
    preserveSymlinks: true
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.ts'],
    setupFiles: ['./vitest.setup.ts'],
    globals: false
  }
})
