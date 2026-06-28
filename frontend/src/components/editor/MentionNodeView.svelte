<script lang="ts">
  // MentionNodeView — inline atomic chip for `@[name]` owner references (#184).
  // Non-editable: backspace deletes the whole chip (atom: true on the schema
  // node). role="mention" conveys the chip is a person reference; the owner
  // name is the accessible name and the decorative `@` glyph is aria-hidden so
  // screen readers announce e.g. "Alice", not "at Alice".
  import { NodeViewWrapper } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node }: NodeViewProps = $props()
  const name = $derived((node.attrs.name as string) || '')
</script>

<NodeViewWrapper as="span">
  <!-- svelte-ignore a11y_unknown_role -->
  <!-- role="mention" is not in the W3C role list yet but is the documented
       contract (#184); no standard ARIA role captures a person reference. -->
  <span class="mention-chip" role="mention" data-name={name} aria-label={name}>
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
      var(--color-accent-primary-start, #4f7cff) 12%,
      transparent
    );
    /* Primary text color (not the accent) keeps the name readable on both dark
       and light themes — accent-on-accent failed contrast on light backgrounds. */
    color: var(--color-text-primary, currentColor);
    font-weight: 500;
    white-space: nowrap;
  }
  .mention-at {
    color: var(--color-accent-primary-start, #4f7cff);
    margin-right: 1px;
  }
</style>
