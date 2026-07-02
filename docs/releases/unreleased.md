# Highlights

- **Plugins can now store their own data.** Plugins get a private database (with vector search) alongside your notes — separate from the core index, never touching your Markdown. The AI plugins arriving next use this for semantic search and summaries; other plugins can use it for caches or memory. Your notes on disk remain the single source of truth.
- **Plugins can contribute custom settings pages.** Plugins with rich configuration (provider setup, action catalogs) now ship a purpose-built Settings tab instead of a flat key/value form. Simple plugins keep the automatic generic form; complex ones opt into a bespoke page.

# Improvements

- **Per-note banners for plugins.** Plugins can surface dismissible highlight banners at the top of a note (e.g. an AI-generated summary), with a consistent close button. Several banners can coexist and stack predictably.
- **Vector search is now built in.** Every plugin database has native cosine-similarity search available, so plugins doing semantic work (search, dedup, retrieval-augmented generation) don't need a separate vector database.

# Notes

- Plugin authors: a new `plugin-db` capability gates the per-plugin store (`ctx.pluginDb.exec/query/migrate`); a new `note-banner` surface kind and a `settingsPageComponent` registration field are available. A plugin declares either a bespoke settings page or the generic settings schema, not both. See `docs/PLUGIN_DEVELOPMENT.md` §8.11–8.13 for the full contracts.
- Core's local-first storage contract is unchanged: Markdown remains the source of truth, and the working-memory index is still fully reproducible from your files. The new plugin storage is a separate, plugin-owned tier deleted on uninstall.
