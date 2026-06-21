<script lang="ts">
  // Test stub for ViewModeToggle. Renders the current mode and a button
  // that fires onToggle so EditorUtilityBar.test.ts can assert the
  // onToggleViewMode prop pass-through via DOM interaction.
  let { mode, onToggle } = $props()
</script>

<div data-testid="view-mode-toggle-stub" data-mode={mode}>
  <button type="button" data-testid="toggle-btn" onclick={() => onToggle()}>
    toggle
  </button>
</div>
