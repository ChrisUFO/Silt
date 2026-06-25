<script lang="ts">
  import type { Editor } from '@tiptap/core'

  // Contextual toolbar for GFM tables (#172). Renders below the format toolbar
  // when the cursor is inside a table cell. Six row/column operations map to
  // TipTap Table commands. Merge cells is omitted — GFM can't represent spans.
  let { editor }: { editor: Editor } = $props()

  type Op = {
    id: string
    icon: string
    label: string
    shortcut?: string
    can: () => boolean
    run: () => void
  }

  // Each op's disabled state depends on the live editor selection (e.g. Merge
  // is only enabled with ≥ 2 cells selected). `editor.can()` reads TipTap
  // state but is not itself reactive, so a plain `$derived` would compute the
  // ops array once and freeze every disabled attribute for the session. We
  // bump a tick on every selection/transaction change and thread it through
  // the derivation so the disabled state stays honest while the cursor remains
  // in the table.
  let selTick = $state(0)
  $effect(() => {
    const handler = (): void => {
      selTick++
    }
    editor.on('selectionUpdate', handler)
    editor.on('transaction', handler)
    return () => {
      editor.off('selectionUpdate', handler)
      editor.off('transaction', handler)
    }
  })

  const ops = $derived.by<Op[]>(() => {
    // selTick is the reactive dependency; reading it invalidates the array
    // (and thus each disabled={!op.can()}) when the selection changes.
    selTick
    return [
      {
        id: 'row-above',
        icon: 'arrow_upward',
        label: 'Insert row above',
        shortcut: 'Ctrl+Shift+Up',
        can: () => !!editor.can().addRowBefore?.(),
        run: () => editor.chain().focus().addRowBefore().run()
      },
      {
        id: 'row-below',
        icon: 'arrow_downward',
        label: 'Insert row below',
        shortcut: 'Ctrl+Shift+Down',
        can: () => !!editor.can().addRowAfter?.(),
        run: () => editor.chain().focus().addRowAfter().run()
      },
      {
        id: 'col-left',
        icon: 'arrow_back',
        label: 'Insert column left',
        shortcut: 'Ctrl+Shift+Left',
        can: () => !!editor.can().addColumnBefore?.(),
        run: () => editor.chain().focus().addColumnBefore().run()
      },
      {
        id: 'col-right',
        icon: 'arrow_forward',
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
        icon: 'delete_outline',
        label: 'Delete column',
        can: () => !!editor.can().deleteColumn?.(),
        run: () => editor.chain().focus().deleteColumn().run()
      }
      // NOTE: "Merge cells" is intentionally omitted — GFM pipe tables cannot
      // represent colspan/rowspan, so a merge would be silently flattened on
      // save and the row structure would change on reload (data corruption).
    ]
  })
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
