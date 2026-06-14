// Public surface of the Silt TipTap editor library. Consumers (TipTapEditor
// component, tests) import from this barrel so the internal file layout can
// evolve without churn at call sites.

export {
  TaskBlock,
  NoteBlock,
  HeaderBlock,
  SiltBlockExtensions
} from './schema'
export { blocksToDoc, docToBlocks } from './converters'
export { UniqueBlockIds, freshId } from './uniqueIdPlugin'
export type {
  ParsedBlock,
  BlockType,
  TaskStatus,
  NodeJSON,
  DocJSON
} from './types'
