<script lang="ts">
  // Test stub for FormatToolbar. Renders a unique data attribute per prop
  // so EditorUtilityBar.test.ts can assert prop pass-through by querying
  // the rendered DOM rather than monkey-patching internal Svelte APIs.
  let { editor, activeMarks, isDark, colorEnabled } = $props()

  const marksStr = $derived(
    activeMarks instanceof Set ? Array.from(activeMarks).sort().join(',') : ''
  )
</script>

<div
  data-testid="format-toolbar-stub"
  data-editor={editor ? 'present' : 'absent'}
  data-active-marks={marksStr}
  data-is-dark={String(isDark)}
  data-color-enabled={String(colorEnabled)}
></div>
