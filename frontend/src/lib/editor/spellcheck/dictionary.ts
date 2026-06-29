import Typo from 'typo-js'

/**
 * typo-js wrapper + the custom-dictionary layer (#196). Loads the bundled
 * en-US Hunspell dictionary on first enable (fetch of index.aff/index.dic from
 * frontend/public/dictionaries/<lang>/), then checks words against it. The
 * per-vault custom dictionary (`editor.custom_dictionary` in config.yaml) is
 * layered as a Set on TOP of typo-js — words present there pass without
 * consulting Hunspell. (typo-js exposes no public addWord, so the custom
 * layer is a separate Set, not a mutation of the Hunspell dictionary table.)
 *
 * A word-result cache avoids re-checking the same token on every debounce.
 * Fully local — no word ever leaves the machine.
 */

let dict: Typo | null = null
let loadPromise: Promise<Typo> | null = null
let currentLang = ''

/** Custom words (lowercased) resolved for the active notebook. */
const customWords = new Set<string>()

/** Session-only ignores (the "Ignore" menu action). Cleared on reload. */
const sessionIgnores = new Set<string>()

/** Word → correctly-spelled cache so unchanged tokens skip Hunspell. */
const cache = new Map<string, boolean>()

/** Load (once per language) and return the Typo instance for `lang`. */
export function loadDictionary(lang: string): Promise<Typo> {
  if (dict && currentLang === lang) return Promise.resolve(dict)
  if (loadPromise && currentLang === lang) return loadPromise
  currentLang = lang
  loadPromise = (async () => {
    try {
      const base = `/dictionaries/${lang}`
      const [aff, dic] = await Promise.all([
        (await fetch(`${base}/index.aff`)).text(),
        (await fetch(`${base}/index.dic`)).text()
      ])
      dict = new Typo(lang, aff, dic)
      cache.clear()
      return dict
    } catch (err) {
      // Fetch can fail in environments without the bundled assets (e.g. jsdom
      // in tests, or a stripped build). Degrade gracefully: leave dict null so
      // checkWord returns true for everything (no false squiggles) rather than
      // surfacing a broken-promise to callers. Logged for diagnosability.
      loadPromise = null
      currentLang = ''
      // eslint-disable-next-line no-console
      console.warn(
        `[silt] spellcheck dictionary "${lang}" failed to load:`,
        err
      )
      throw err
    }
  })()
  return loadPromise
}

/** True once the dictionary for the current language has finished loading. */
export function isDictionaryLoaded(): boolean {
  return dict !== null && dict.loaded
}

/**
 * Replace the active custom-word set. Called when the config loads / changes
 * and when a word is added/removed via IPC. Clears the word cache so tokens
 * previously flagged as misspelled are re-evaluated.
 */
export function setCustomWords(words: string[]): void {
  customWords.clear()
  for (const w of words) {
    const lower = w.trim().toLowerCase()
    if (lower) customWords.add(lower)
  }
  cache.clear()
}

/** Whether `word` is known-correct (custom dict OR session-ignore OR Hunspell). */
export function checkWord(word: string): boolean {
  if (!dict) return true // not loaded yet — don't flag (avoids a false wave)
  const lower = word.toLowerCase()
  if (customWords.has(lower) || sessionIgnores.has(lower)) return true
  const cached = cache.get(lower)
  if (cached !== undefined) return cached
  const result = dict.check(lower)
  cache.set(lower, result)
  return result
}

/** Ignore a word for the current session only (the "Ignore" menu action). */
export function ignoreWordSession(word: string): void {
  const lower = word.trim().toLowerCase()
  if (lower) {
    sessionIgnores.add(lower)
    cache.delete(lower)
  }
}

/** Top-N Hunspell suggestions for a misspelled word (empty if none). */
export function suggest(word: string, limit = 5): string[] {
  if (!dict) return []
  const suggestions = dict.suggest(word.toLowerCase())
  return suggestions.slice(0, limit)
}
