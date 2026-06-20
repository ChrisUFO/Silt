// silt-attachments plugin entry (#101). Registers the /attach slash command
// via ctx.registerSlashCommand. The command opens a native file picker, copies
// the chosen file into the notebook's attachments/ dir, and inserts an
// embedBlock at the cursor. Uses the PluginContext SDK exclusively.
//
// Kanban travel (#101): an attachment embedBlock inserted as a CHILD of a
// task block (indented under it) automatically travels with its parent when
// the task is reordered in the editor or queried in the Kanban. This is
// inherent to the block hierarchy — the parser preserves parent-child depth,
// and the Kanban's query joins on blocks.parent_id. No explicit association
// model is needed; the block tree IS the association.
import type { PluginContext, SiltPlugin } from '../../sdk'
import Attachments from './Attachments.svelte'

export const manifest = {
  id: 'silt-attachments',
  name: 'Attachments',
  version: '1.0.0',
  author: 'Silt',
  description:
    'Attach files to notes via /attach. Copies into the notebook; opens in the OS native handler.',
  icon: 'attach_file'
}

// The /attach command handler. `editor` is the live TipTap instance; `pos` is
// the cursor position. The handler:
// 1. Opens the native file picker (ctx.pickOpenFile).
// 2. Copies the file into attachments/ via ctx.addAttachment (returns relPath).
// 3. Inserts an embedBlock node at the cursor with the attachment attrs.
async function handleAttach(
  ctx: PluginContext,
  editor: any,
  _pos: number
): Promise<void> {
  const notebook = ctx.activeNotebook
  if (!notebook) return
  const src = await ctx.pickOpenFile('*')
  if (!src) return // user cancelled
  const relPath = await ctx.addAttachment(src, notebook)
  if (!relPath) return

  const fileName = relPath.split('/').pop() || relPath
  const isImage = /\.(png|jpe?g|gif|webp|svg|bmp)$/i.test(relPath)
  // Insert an embedBlock node via the editor's insertContent. The node carries
  // the attachment attrs; the embedBlock marker round-trips through the parser.
  const embedBlockNode = {
    type: 'embedBlock',
    attrs: {
      embedType: isImage ? 'image' : 'attachment',
      src: relPath,
      caption: fileName,
      openable: true,
      pluginID: 'silt-attachments'
    }
  }
  if (editor && !editor.isDestroyed) {
    editor.commands.insertContent(embedBlockNode)
    editor.commands.focus()
  }
}

export default {
  manifest,
  component: Attachments,
  onVaultOpen(ctx: PluginContext) {
    // Register the /attach slash command. The returned unregister is captured
    // but cleanup also happens via unregisterPluginSlashCommands on teardown.
    ctx.registerSlashCommand({
      id: 'attach',
      label: 'Attach File',
      description: 'Pick a file and embed it in the note',
      icon: 'attach_file',
      onSelect: (editor, pos) => {
        handleAttach(ctx, editor, pos).catch((e) => {
          // eslint-disable-next-line no-console
          console.error('[silt-attachments] /attach failed:', e)
        })
      }
    })
  }
} satisfies SiltPlugin & { component: unknown }
