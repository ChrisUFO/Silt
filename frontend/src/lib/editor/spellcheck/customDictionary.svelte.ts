import {
  GetCustomDictionary,
  AddCustomDictionaryWord,
  RemoveCustomDictionaryWord
} from '../../../../wailsjs/go/main/App.js'

/**
 * Reactive custom-spellcheck-dictionary store (#196). Backs both the editor
 * (the spellcheck layer reads editor.custom_dictionary from settings.config)
 * and the Settings → Editor "Custom dictionary" management card (view/add/
 * remove). Reads + mutates via the atomic-config IPC in app_spellcheck.go;
 * the config:changed event then refreshes settings.config.editor.custom_dictionary,
 * which the editor's $effect (TipTapEditor) picks up to re-apply.
 *
 * v1 manages the VAULT dictionary (the common case). Linked-notebook co-located
 * overrides (Sprint 34) are a follow-up.
 */
let words = $state<string[]>([])
let filter = $state('')
let newWord = $state('')
let loading = $state(false)
let error = $state<string | null>(null)

export const customDictionary = {
  get words() {
    return words
  },
  get filter() {
    return filter
  },
  set filter(v: string) {
    filter = v
  },
  get newWord() {
    return newWord
  },
  set newWord(v: string) {
    newWord = v
  },
  get loading() {
    return loading
  },
  get error() {
    return error
  },
  /** Filtered view for the management-card list. */
  get filtered() {
    const f = filter.trim().toLowerCase()
    if (!f) return words
    return words.filter((w) => w.toLowerCase().includes(f))
  },

  /** Load the resolved list from the backend. Called on card open. */
  async load(): Promise<void> {
    loading = true
    error = null
    try {
      words = await GetCustomDictionary()
    } catch (e) {
      error = String(e)
    } finally {
      loading = false
    }
  },

  /** Add the current newWord (or a passed-in word) via the IPC. */
  async add(word?: string): Promise<void> {
    const w = (word ?? newWord).trim()
    if (!w) return
    error = null
    try {
      words = await AddCustomDictionaryWord(w)
      if (!word) newWord = ''
    } catch (e) {
      error = String(e)
    }
  },

  /** Remove a word via the IPC. */
  async remove(word: string): Promise<void> {
    error = null
    try {
      words = await RemoveCustomDictionaryWord(word)
    } catch (e) {
      error = String(e)
    }
  }
}
