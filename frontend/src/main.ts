import './index.css'
import { mount } from 'svelte'
import App from './App.svelte'
import { initTheme } from './theme/store.svelte'

// Start the theme engine as early as possible: it fetches the active theme
// over IPC and injects it onto :root with a same-tick repaint, overriding
// the index.css :root startup fallbacks. Not awaited so the shell renders
// immediately from the fallbacks; the injector repaints the moment IPC
// returns.
initTheme()

const app = mount(App, {
  target: document.getElementById('app')!
})

export default app
