// Shared no-op stubs for the v2 SDK PluginContext methods, used by test/mock
// context builders so every mock does not have to repeat 27 no-op closures.
// Production code builds the real context via makePluginContext (context.ts).

import type { PluginContext, SqliteQueryResult } from './sdk'

const emptyResult: SqliteQueryResult = { rows: [], truncated: false }

/**
 * Default no-op implementations of the v2 SDK content/file/OS methods. Spread
 * this into a mock context and override only the methods a test exercises.
 */
export const v2CtxStubs: Pick<
  PluginContext,
  | 'queryByTag'
  | 'queryByDateRange'
  | 'fullTextSearch'
  | 'getBacklinks'
  | 'getEmbeds'
  | 'createBlock'
  | 'deleteBlock'
  | 'moveBlock'
  | 'createPage'
  | 'createSection'
  | 'createNotebook'
  | 'deletePage'
  | 'renamePage'
  | 'readFile'
  | 'writeFile'
  | 'deleteFile'
  | 'listDir'
  | 'notebookRoot'
  | 'scratchDir'
  | 'openInNativeHandler'
  | 'openUrl'
  | 'pickOpenFile'
  | 'pickSaveFile'
  | 'clipboardRead'
  | 'clipboardWrite'
  | 'notify'
  | 'fetch'
  | 'registerSlashCommand'
  | 'provideDecorations'
  | 'getSetting'
  | 'registerSurface'
  | 'addAttachment'
  | 'openAttachment'
  | 'deleteAttachment'
  | 'vaultScratchDir'
  | 'resolveAsset'
  | 'readPluginAsset'
  | 'getNavigationTree'
> = {
  queryByTag: () => Promise.resolve(emptyResult),
  queryByDateRange: () => Promise.resolve(emptyResult),
  fullTextSearch: () => Promise.resolve(emptyResult),
  getBacklinks: () => Promise.resolve(emptyResult),
  getEmbeds: () => Promise.resolve(emptyResult),
  createBlock: () => Promise.resolve(''),
  deleteBlock: () => Promise.resolve(true),
  moveBlock: () => Promise.resolve(true),
  createPage: () => Promise.resolve(''),
  createSection: () => Promise.resolve(true),
  createNotebook: () => Promise.resolve(true),
  deletePage: () => Promise.resolve(true),
  renamePage: () => Promise.resolve(true),
  readFile: () => Promise.resolve(new Uint8Array(0)),
  writeFile: () => Promise.resolve(true),
  deleteFile: () => Promise.resolve(true),
  listDir: () => Promise.resolve([]),
  notebookRoot: () => Promise.resolve(''),
  scratchDir: () => Promise.resolve(''),
  openInNativeHandler: () => Promise.resolve(true),
  openUrl: () => Promise.resolve(true),
  pickOpenFile: () => Promise.resolve(''),
  pickSaveFile: () => Promise.resolve(''),
  clipboardRead: () => Promise.resolve(''),
  clipboardWrite: () => Promise.resolve(true),
  notify: () => Promise.resolve(true),
  fetch: () =>
    Promise.resolve({
      status: 0,
      headers: {},
      body: '',
      ok: false,
      truncated: false
    }),
  registerSlashCommand: () => () => {},
  provideDecorations: () => () => {},
  getSetting: () => Promise.resolve(undefined),
  registerSurface: () => () => {},
  addAttachment: () => Promise.resolve(''),
  openAttachment: () => Promise.resolve(true),
  deleteAttachment: () => Promise.resolve(true),
  vaultScratchDir: () => Promise.resolve(''),
  resolveAsset: () => Promise.resolve(''),
  readPluginAsset: () => Promise.resolve(''),
  getNavigationTree: () => Promise.resolve({ notebooks: [] })
}
