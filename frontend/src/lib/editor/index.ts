// Public surface of the Silt TipTap editor library. Consumers (TipTapEditor
// component, tests) import from this barrel so the internal file layout can
// evolve without churn at call sites.

export {
  TaskBlock,
  NoteBlock,
  HeaderBlock,
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  SiltColorMarkExtensions
} from './schema'
export { SiltBlockExtensionsWithNodeViews } from './nodeViews'
export { blocksToDoc, docToBlocks } from './converters'
export { UniqueBlockIds, freshId } from './uniqueIdPlugin'
export { SiltBlockKeymaps, convertToBlock } from './keymaps'
export {
  TaskMetaSuggest,
  applyMetaSuggestion,
  filterMetaKeys,
  getSuggestContext,
  getMetaSuggestState,
  META_KEYS
} from './taskMetaSuggest'
export type {
  MetaKey,
  SuggestContext,
  InsertPlan,
  TaskMetaSuggestOptions
} from './taskMetaSuggest'
export type {
  ParsedBlock,
  BlockType,
  TaskStatus,
  NodeJSON,
  DocJSON
} from './types'
