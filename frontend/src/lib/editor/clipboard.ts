import type { Editor } from 'svelte-tiptap'
import type { Node as ProseMirrorNode } from '@tiptap/pm/model'
import { serializeInlineContent } from './converters'
import { findActiveBlock } from './keymaps'
import { pushNotification } from '../../notifications/store.svelte'

/**
 * Context-menu state shape the clipboard handlers read from. The host owns
 * this state (because the context menu also drives focus / Escape / outside-
 * click handling); clipboard just reads the active-block fields.
 */
export interface ClipboardMenuState {
  activeBlockId?: string
  activeBlockNode?: ProseMirrorNode
}

/**
 * Bag the host passes in. `notify` is the toast pusher; `menu()` returns the
 * current menu state (or null) so the handlers see live values without
 * holding a stale snapshot.
 */
export interface ClipboardDeps {
  editor: Editor
  notify: typeof pushNotification
  menu: () => ClipboardMenuState | null
}

/** writeText but never throws — clipboard permissions are best-effort. */
async function writeTextSilent(text: string): Promise<void> {
  await navigator.clipboard.writeText(text).catch(() => {})
}

export function cutSelection(deps: ClipboardDeps): void {
  const { editor } = deps
  const { selection } = editor.state
  const text = editor.state.doc.textBetween(selection.from, selection.to, '\n')
  void writeTextSilent(text)
  editor.commands.deleteSelection()
}

export function copySelection(deps: ClipboardDeps): void {
  const { editor } = deps
  const { selection } = editor.state
  const text = editor.state.doc.textBetween(selection.from, selection.to, '\n')
  void writeTextSilent(text)
}

export async function pasteFromClipboard(deps: ClipboardDeps): Promise<void> {
  const { editor, notify } = deps
  try {
    const text = await navigator.clipboard.readText()
    if (text) {
      editor.commands.insertContent({ type: 'text', text })
    }
  } catch {
    notify({
      kind: 'error',
      message: 'Paste failed: clipboard could not be read.'
    })
  }
}

/**
 * Serialize the current selection (or the active block, if the selection is
 * empty) to markdown via `serializeInlineContent` and write it to the
 * clipboard. The empty-selection-block path is what the right-click "Copy as
 * Markdown" menu item drives when the user hasn't selected anything.
 */
export async function copyAsMarkdown(deps: ClipboardDeps): Promise<void> {
  const { editor, menu } = deps
  const { selection } = editor.state
  let md = ''
  if (selection.empty) {
    const active = menu()?.activeBlockNode
    if (active) {
      const json = active.toJSON()
      md = json.content ? serializeInlineContent(json.content) : ''
    }
  } else {
    const slice = editor.state.doc.slice(selection.from, selection.to)
    const parts: string[] = []
    slice.content.forEach((node) => {
      const json = node.toJSON()
      parts.push(
        json.content ? serializeInlineContent(json.content) : json.text || ''
      )
    })
    md = parts.join('\n')
  }
  await writeTextSilent(md)
}

export async function copyAsPlainText(deps: ClipboardDeps): Promise<void> {
  const { editor } = deps
  const { selection } = editor.state
  const text = selection.empty
    ? ''
    : editor.state.doc.textBetween(selection.from, selection.to, '\n')
  await writeTextSilent(text)
}

export async function copyBlockReference(deps: ClipboardDeps): Promise<void> {
  const id = deps.menu()?.activeBlockId
  if (id) {
    await writeTextSilent(`((${id}))`)
  }
}

export async function copyBlockEmbed(deps: ClipboardDeps): Promise<void> {
  const id = deps.menu()?.activeBlockId
  if (id) {
    await writeTextSilent(`{{embed:${id}}}`)
  }
}

/**
 * Insert a copy of the active block node immediately after it. The cloned
 * node's `id` attr is stripped so the unique-id plugin mints a fresh UUID
 * (otherwise the round-trip through markdown would emit two blocks with the
 * same id and the indexer would dedupe them).
 */
export function duplicateBlock(deps: ClipboardDeps): void {
  const { editor, menu } = deps
  const active = menu()?.activeBlockNode
  if (!active) return
  const block = findActiveBlock(editor)
  if (!block) return
  const endPos = editor.state.selection.$from.after(block.depth)
  const json = active.toJSON()
  if (json.attrs && json.attrs.id) {
    delete json.attrs.id
  }
  editor.chain().insertContentAt(endPos, json).focus().run()
}

/**
 * Delete the active block. Refuses to delete the last remaining block in the
 * doc so the editor is never empty (ProseMirror requires at least one node).
 */
export function deleteBlock(deps: ClipboardDeps): void {
  const { editor } = deps
  const { doc } = editor.state
  if (doc.childCount <= 1) return
  const block = findActiveBlock(editor)
  if (!block) return
  const pos = editor.state.selection.$from
  const from = pos.before(block.depth)
  const to = pos.after(block.depth)
  editor.chain().deleteRange({ from, to }).focus().run()
}
