# Silt bundled dictionaries

Hunspell-format dictionaries bundled with Silt for the inline spellcheck
feature (#196). Loaded on demand by `frontend/src/lib/editor/spellcheck/dictionary.ts`
via `fetch('/dictionaries/<lang>/index.aff')` and `index.dic`.

## Layout

```
dictionaries/
  <lang>/          # BCP-47-ish tag matching config editor.spellcheck_language
    index.aff      # Hunspell affix rules
    index.dic      # Hunspell word list
```

v1 ships `en-US` only. Additional languages are a follow-up (lazy-loaded from
the same path or a user-drop folder).

## Source & license

The `en-US` files are sourced verbatim from
[`wooorm/dictionaries`](https://github.com/wooorm/dictionaries)
(`dictionaries/en/index.aff` + `index.dic`), which repackages the upstream
Hunspell/LibreOffice English dictionary.

- **Engine**: [`typo-js`](https://github.com/cfinke/Typo.js) (Modified BSD) —
  pure-JS Hunspell, ~8 KB, runs in the Wails webview.
- **Dictionary (`en`)**: `(MIT AND BSD)` per `wooorm/dictionaries` — compatible
  with bundling in a closed-source desktop app. See
  <https://github.com/wooorm/dictionaries> for the full license text.

Spellcheck is fully local: no word ever leaves the user's machine.
