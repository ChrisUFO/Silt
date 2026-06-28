<script lang="ts">
  // MentionNodeView — inline atomic chip for `@[name]` owner references (#184).
  // Non-editable: backspace deletes the whole chip (atom: true on the schema
  // node). The owner name is the accessible name; the decorative `@` glyph is
  // aria-hidden so screen readers announce e.g. "Alice", not "at Alice".
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node }: NodeViewProps = $props()
  const name = $derived((node.attrs.name as string) || '')
</script>

<NodeViewWrapper as="span">
  <span class="mention-chip" data-name={name} aria-label={name}>
    <span class="mention-at" aria-hidden="true">@</span>{name}
  </span>
</NodeViewWrapper>

<style>
  .mention-chip {
    display: inline;
    padding: 0 2px;
    border-radius: 4px;
    background: color-mix(
      in srgb,
      var(--color-accent-primary-start, #4f7cff) 18%,
      transparent
    );
    color: var(--color-accent-primary-start, #4f7cff);
    font-weight: 500;
    white-space: nowrap;
  }
  .mention-at {
    opacity: 0.7;
    margin-right: 1px;
  }
</style>
