<script lang="ts">
  import type { Editor } from '@tiptap/core'
  import { nearestEnabledIndex } from '../../lib/editor/rovingTabindex'

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

  // Roving tabindex (a11y): one button in the toolbar holds tabindex 0, the
  // rest hold -1, so the whole toolbar is a single Tab stop and Arrow keys move
  // between its buttons. Mirrors FormatToolbar.svelte. Disabled buttons are
  // skipped while roving (a disabled control can't take focus in most browsers,
  // so landing on one would stall the row).
  let rovingIdx = $state(0)
  let toolbarEl: HTMLElement | null = $state(null)

  // Re-clamp the roving focus when button enabled-states change: if the
  // button holding tabindex 0 becomes disabled (e.g. table shrinks to one
  // row so "Delete row" greys out), move the Tab-stop to the nearest
  // enabled button so keyboard users never land on an inert control.
  $effect(() => {
    const currentOps = ops
    if (rovingIdx >= currentOps.length || !currentOps[rovingIdx].can()) {
      const disabled = currentOps.map((op) => !op.can())
      const next = nearestEnabledIndex(disabled, rovingIdx, 1)
      if (next !== rovingIdx) rovingIdx = next
    }
  })

  function handleKeydown(e: KeyboardEvent): void {
    const btns = toolbarEl?.querySelectorAll<HTMLButtonElement>('[data-tb]')
    if (!btns || btns.length === 0) return
    const disabled = Array.from(btns, (b) => b.disabled)
    let next = rovingIdx
    if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      e.preventDefault()
      next = nearestEnabledIndex(disabled, rovingIdx, 1)
    } else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      e.preventDefault()
      next = nearestEnabledIndex(disabled, rovingIdx, -1)
    } else if (e.key === 'Home') {
      e.preventDefault()
      next = nearestEnabledIndex(disabled, -1, 1)
    } else if (e.key === 'End') {
      e.preventDefault()
      next = nearestEnabledIndex(disabled, disabled.length, -1)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      editor.chain().focus().run()
      return
    } else {
      return
    }
    rovingIdx = next
    btns[next]?.focus()
  }
</script>

<div
  class="table-context-toolbar"
  role="toolbar"
  aria-label="Table actions"
  tabindex="-1"
  bind:this={toolbarEl}
  onkeydown={handleKeydown}
>
  {#each ops as op, i (op.id)}
    <button
      type="button"
      class="tct-btn"
      data-tb
      disabled={!op.can()}
      aria-label={op.label}
      aria-keyshortcuts={op.shortcut}
      tabindex={rovingIdx === i ? 0 : -1}
      title={op.shortcut ? `${op.label} (${op.shortcut})` : op.label}
      onclick={op.run}
      onfocus={() => (rovingIdx = i)}
    >
      <span class="material-symbols-outlined" aria-hidden="true">
        {op.icon}
      </span>
    </button>
  {/each}
</div>
