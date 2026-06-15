<script lang="ts">
  interface Props {
    width: number
    min?: number
    max?: number
    defaultWidth?: number
    onWidthChange: (px: number) => void
    onWidthCommit: (px: number) => void
  }

  let {
    width,
    min = 200,
    max = 480,
    defaultWidth = 256,
    onWidthChange,
    onWidthCommit
  }: Props = $props()

  let handleEl: HTMLButtonElement
  let dragging = $state(false)

  function clamp(px: number): number {
    return Math.max(min, Math.min(max, px))
  }

  function handlePointerDown(e: PointerEvent) {
    e.preventDefault()
    handleEl.setPointerCapture(e.pointerId)
    dragging = true
    const startX = e.clientX
    const startWidth = width

    function onMove(ev: PointerEvent) {
      const delta = ev.clientX - startX
      onWidthChange(clamp(startWidth + delta))
    }
    function onUp(ev: PointerEvent) {
      handleEl.releasePointerCapture(ev.pointerId)
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      const delta = ev.clientX - startX
      const finalWidth = clamp(startWidth + delta)
      dragging = false
      onWidthCommit(finalWidth)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  function handleDoubleClick() {
    onWidthCommit(defaultWidth)
  }

  function handleKeyDown(e: KeyboardEvent) {
    const step = e.shiftKey ? 32 : 8
    let newWidth = width
    switch (e.key) {
      case 'ArrowLeft':
        newWidth = clamp(width - step)
        break
      case 'ArrowRight':
        newWidth = clamp(width + step)
        break
      case 'Home':
        newWidth = min
        break
      case 'End':
        newWidth = max
        break
      case 'Enter':
        newWidth = defaultWidth
        break
      default:
        return
    }
    e.preventDefault()
    onWidthChange(newWidth)
    onWidthCommit(newWidth)
  }
</script>

<button
  type="button"
  bind:this={handleEl}
  aria-label="Resize sidebar (drag, double-click to reset, or use arrow keys)"
  title="Drag to resize · Double-click to reset · Arrow keys to nudge"
  tabindex="0"
  onpointerdown={handlePointerDown}
  ondblclick={handleDoubleClick}
  onkeydown={handleKeyDown}
  class="sidebar-resize-handle"
  class:dragging
  style="touch-action: none; flex-shrink: 0;"
></button>

<style>
  .sidebar-resize-handle {
    width: 4px;
    height: 100%;
    cursor: col-resize;
    background-color: var(--border-muted);
    border: none;
    padding: 0;
    margin: 0;
    border-radius: 0;
    transition: background-color 120ms ease-out;
    z-index: 45;
    position: relative;
    -webkit-appearance: none;
    appearance: none;
  }
  .sidebar-resize-handle:hover,
  .sidebar-resize-handle.dragging {
    background-color: var(--accent-primary-start);
  }
  .sidebar-resize-handle:focus-visible {
    outline: 2px solid var(--accent-primary-start);
    outline-offset: -1px;
  }
</style>
