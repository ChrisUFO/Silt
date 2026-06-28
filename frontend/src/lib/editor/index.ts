// Public surface of the Silt TipTap editor library. Consumers (TipTapEditor
// component, tests) import from this barrel so the internal file layout can
// evolve without churn at call sites.

export {
  TaskBlock,
  NoteBlock,
  HeaderBlock,
  CalloutBlock,
  CodeBlock,
  MentionNode,
  SiltBlockExtensions,
  SiltInlineMarkExtensions,
  SiltColorMarkExtensions,
  SiltDetailsExtensions,
  SiltTableExtensions
} from './schema'
export { SiltBlockExtensionsWithNodeViews } from './nodeViews'
export { blocksToDoc, docToBlocks } from './converters'
export { UniqueBlockIds, freshId } from './uniqueIdPlugin'
export {
  SiltBlockKeymaps,
  convertToBlock,
  setBlockAlign,
  toggleBlockQuote,
  insertCallout,
  insertCodeBlock,
  insertDetails,
  insertTable,
  toggleDetails,
  findActiveBlock,
  BLOCK_TYPES
} from './keymaps'
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
export {
  MentionSuggest,
  applyMentionSuggestion,
  filterOwners,
  getMentionContext,
  getMentionSuggestState
} from './mentionSuggest'
export type { MentionContext, MentionSuggestOptions } from './mentionSuggest'
export type {
  ParsedBlock,
  BlockType,
  TaskStatus,
  NodeJSON,
  DocJSON
} from './types'
