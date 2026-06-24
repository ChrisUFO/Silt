<script lang="ts">
  import type { Editor } from '@tiptap/core'

  // Contextual toolbar for GFM tables (#172). Renders below the format toolbar
  // when the cursor is inside a table cell. The seven row/column operations
  // map to TipTap Table commands; Merge is enabled only when ≥ 2 cells are
  // selected (mergeCells requires a multi-cell selection).
  let { editor }: { editor: Editor } = $props()

  type Op = {
    id: string
    icon: string
    label: string
    shortcut?: string
    can: () => boolean
    run: () => void
  }

  const ops = $derived<Op[]>([
    {
      id: 'row-above',
      icon: 'add_row_above',
      label: 'Insert row above',
      shortcut: 'Ctrl+Shift+Up',
      can: () => !!editor.can().addRowBefore?.(),
      run: () => editor.chain().focus().addRowBefore().run()
    },
    {
      id: 'row-below',
      icon: 'add_row_below',
      label: 'Insert row below',
      shortcut: 'Ctrl+Shift+Down',
      can: () => !!editor.can().addRowAfter?.(),
      run: () => editor.chain().focus().addRowAfter().run()
    },
    {
      id: 'col-left',
      icon: 'add_column_left',
      label: 'Insert column left',
      shortcut: 'Ctrl+Shift+Left',
      can: () => !!editor.can().addColumnBefore?.(),
      run: () => editor.chain().focus().addColumnBefore().run()
    },
    {
      id: 'col-right',
      icon: 'add_column_right',
      label: 'Insert column right',
      shortcut: 'Ctrl+Shift+Right',
      can: () => !!editor.can().addColumnAfter?.(),
      run: () => editor.chain().focus().addColumnAfter().run()
    },
    {
      id: 'del-row',
      icon: 'delete',
      label: 'Delete row',
      can: () => !!editor.can().deleteRow?.(),
      run: () => editor.chain().focus().deleteRow().run()
    },
    {
      id: 'del-col',
      icon: 'delete_sweep',
      label: 'Delete column',
      can: () => !!editor.can().deleteColumn?.(),
      run: () => editor.chain().focus().deleteColumn().run()
    },
    {
      id: 'merge',
      icon: 'merge',
      label: 'Merge cells',
      can: () => !!editor.can().mergeCells?.(),
      run: () => editor.chain().focus().mergeCells().run()
    }
  ])
</script>

<div class="table-context-toolbar" role="toolbar" aria-label="Table actions">
  {#each ops as op (op.id)}
    <button
      type="button"
      class="tct-btn"
      disabled={!op.can()}
      aria-label={op.label}
      aria-keyshortcuts={op.shortcut}
      title={op.shortcut ? `${op.label} (${op.shortcut})` : op.label}
      onclick={op.run}
    >
      <span class="material-symbols-outlined" aria-hidden="true">
        {op.icon}
      </span>
    </button>
  {/each}
</div>
