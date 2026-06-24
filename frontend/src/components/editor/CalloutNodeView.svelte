<script lang="ts">
  import { NodeViewWrapper, NodeViewContent } from 'svelte-tiptap'
  import type { NodeViewProps } from '@tiptap/core'

  let { node, updateAttributes }: NodeViewProps = $props()
  let variant = $derived(node.attrs.variant || 'note')
  let title = $derived(node.attrs.title || '')
  let titleEl: HTMLSpanElement | null = $state(null)

  function onTitleInput(): void {
    if (titleEl) {
      updateAttributes({ title: titleEl.textContent || '' })
    }
  }

  let variantIcon = $derived.by(() => {
    const icons: Record<string, string> = {
      note: 'info',
      info: 'information',
      tip: 'lightbulb',
      warning: 'warning',
      danger: 'error',
      success: 'check_circle',
      quote: 'format_quote'
    }
    return icons[variant] || 'info'
  })

  let borderColor = $derived.by(() => {
    const colors: Record<string, string> = {
      note: 'border-l-accent-primary',
      info: 'border-l-accent-tertiary',
      tip: 'border-l-accent-secondary',
      warning: 'border-l-status-warn',
      danger: 'border-l-status-danger',
      success: 'border-l-status-success',
      quote: 'border-l-text-muted'
    }
    return colors[variant] || 'border-l-accent-primary'
  })

  let bgColor = $derived.by(() => {
    const colors: Record<string, string> = {
      note: 'bg-accent-primary/5',
      info: 'bg-accent-tertiary/5',
      tip: 'bg-accent-secondary/5',
      warning: 'bg-status-warn/5',
      danger: 'bg-status-danger/5',
      success: 'bg-status-success/5',
      quote: 'bg-text-muted/5'
    }
    return colors[variant] || 'bg-accent-primary/5'
  })

  let iconColor = $derived.by(() => {
    const colors: Record<string, string> = {
      note: 'text-accent-primary',
      info: 'text-accent-tertiary',
      tip: 'text-accent-secondary',
      warning: 'text-status-warn',
      danger: 'text-status-danger',
      success: 'text-status-success',
      quote: 'text-text-muted'
    }
    return colors[variant] || 'text-accent-primary'
  })

  let roleAttr = $derived(
    variant === 'danger' || variant === 'warning' ? 'alert' : 'note'
  )
  let emptyBody = $derived(!node.content.size || node.textContent.trim() === '')
</script>

<NodeViewWrapper
  class="callout-block my-2 rounded-lg border-l-4 px-4 py-3 {borderColor} {bgColor}"
  data-variant={variant}
  role={roleAttr}
  aria-label={title || variant}
>
  <div class="flex items-start gap-2 mb-1">
    <span
      class="material-symbols-outlined text-[20px] {iconColor} flex-shrink-0 mt-0.5 select-none"
      aria-hidden="true"
    >
      {variantIcon}
    </span>
    <span
      bind:this={titleEl}
      class="font-semibold text-sm leading-6 text-text outline-none min-w-[20px]"
      class:opacity-50={!title}
      contenteditable="true"
      role="textbox"
      aria-label="Callout title"
      data-placeholder="Title"
      oninput={onTitleInput}
    >
      {title || ''}
    </span>
  </div>
  <div class="pl-7">
    <NodeViewContent
      class="whitespace-pre-wrap break-words min-h-[22px] focus:outline-none text-text text-sm"
      data-empty={emptyBody || undefined}
      style={emptyBody ? 'opacity: 0.5' : ''}
    />
  </div>
</NodeViewWrapper>
