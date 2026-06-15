<script lang="ts">
  // FontSelect — a custom combobox for the preloaded font picker (#82).
  //
  // A native <select> renders <option style="font-family:…"> unreliably on
  // some webviews (notably macOS WebKit), and with ~26 bundled families the
  // in-font preview is the whole point. This button + popup listbox renders
  // every option as a real element styled in its own font, groups by
  // category, surfaces a "Theme default" / unlisted-current-value leading
  // option, and is keyboard-navigable (Arrow/Home/End/Enter/Escape) with
  // ARIA combobox/listbox semantics.
  import { createEventDispatcher } from 'svelte'
  import {
    bundledByCategory,
    systemFonts,
    findByCssFamily,
    displayFamilyName,
    type FontEntry
  } from '../../theme/fonts'

  type Category = 'body' | 'mono'

  let {
    value = $bindable(''),
    category,
    themeFont = '',
    label,
    onchange
  }: {
    value: string
    category: Category
    themeFont?: string
    label: string
    onchange?: () => void
  } = $props()

  const dispatch = createEventDispatcher()

  // Option groups for this field. Body may use sans/display/serif; mono is
  // mono-only. System fallbacks sit in their own group.
  let groups = $derived.by<Array<{ label: string; items: FontEntry[] }>>(() => {
    const sys = systemFonts()
    if (category === 'mono') {
      return [
        { label: 'Monospace', items: bundledByCategory('mono') },
        { label: 'System', items: sys.filter((f) => f.category === 'mono') }
      ]
    }
    return [
      { label: 'Sans-serif', items: bundledByCategory('sans') },
      { label: 'Display', items: bundledByCategory('display') },
      { label: 'Serif', items: bundledByCategory('serif') },
      { label: 'System', items: sys.filter((f) => f.category === 'sans') }
    ]
  })

  interface Option {
    value: string
    label: string
    cssFamily: string // for in-font preview; '' = no special font
    group: string
  }

  // A leading "Theme default" option appears only when the active theme
  // overrides this slot; an "Inherit" option covers the empty-without-override
  // case so the trigger never misrepresents the state.
  let leadingOption = $derived.by<Option | null>(() => {
    if (themeFont) {
      return {
        value: '',
        label: `Theme default (${displayFamilyName(themeFont)})`,
        cssFamily: themeFont,
        group: ''
      }
    }
    if (value === '') {
      return { value: '', label: 'Inherit (no theme default)', cssFamily: '', group: '' }
    }
    return null
  })

  // A config value that isn't curated (legacy/hand-edited) gets a synthetic
  // option so the dropdown shows the real current value rather than the first
  // list entry.
  let unlistedOption = $derived.by<Option | null>(() => {
    if (value !== '' && !findByCssFamily(value)) {
      return {
        value,
        label: `${displayFamilyName(value)} (custom)`,
        cssFamily: value,
        group: ''
      }
    }
    return null
  })

  // Flat option list (leading, unlisted, then grouped) for indexing.
  let options = $derived.by<Option[]>(() => {
    const out: Option[] = []
    if (leadingOption) out.push(leadingOption)
    if (unlistedOption) out.push(unlistedOption)
    for (const g of groups) {
      for (const f of g.items) {
        out.push({ value: f.cssFamily, label: f.displayName, cssFamily: f.cssFamily, group: g.label })
      }
    }
    return out
  })

  let selectedIndex = $derived(Math.max(0, options.findIndex((o) => o.value === value)))

  // Trigger display.
  let triggerLabel = $derived.by(() => {
    const o = options[selectedIndex]
    return o ? o.label : 'Select…'
  })
  let triggerFontStyle = $derived.by(() => {
    const o = options[selectedIndex]
    if (!o || !o.cssFamily) return ''
    return `font-family: ${o.cssFamily}`
  })

  let open = $state(false)
  let activeIndex = $state(0)
  let rootEl: HTMLDivElement | null = $state(null)
  let optionEls: HTMLButtonElement[] = $state([])

  function toggle() {
    open = !open
    if (open) activeIndex = selectedIndex
  }

  function commit(index: number) {
    const o = options[index]
    if (!o) return
    value = o.value
    open = false
    onchange?.()
    dispatch('change')
  }

  function onTriggerKey(e: KeyboardEvent) {
    if (!open && (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault()
      open = true
      activeIndex = selectedIndex
      queueMicrotask(() => optionEls[activeIndex]?.focus())
      return
    }
  }

  function onOptionKey(e: KeyboardEvent, index: number) {
    const last = options.length - 1
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
      e.preventDefault()
      activeIndex = Math.min(last, index + 1)
      optionEls[activeIndex]?.focus()
    } else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
      e.preventDefault()
      activeIndex = Math.max(0, index - 1)
      optionEls[activeIndex]?.focus()
    } else if (e.key === 'Home') {
      e.preventDefault()
      activeIndex = 0
      optionEls[0]?.focus()
    } else if (e.key === 'End') {
      e.preventDefault()
      activeIndex = last
      optionEls[last]?.focus()
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      commit(index)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      open = false
    }
  }

  function onWindowClick(e: MouseEvent) {
    if (open && rootEl && !rootEl.contains(e.target as Node)) {
      open = false
    }
  }
</script>

<svelte:window onclick={onWindowClick} />

<div class="relative flex-1" bind:this={rootEl}>
  <button
    type="button"
    role="combobox"
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-label={label}
    onclick={toggle}
    onkeydown={onTriggerKey}
    class="w-full flex items-center justify-between gap-2 bg-bg-surface border border-border-zinc rounded-lg px-3 py-2 text-text-primary text-[13px] font-body-md outline-none focus:border-accent-primary-start transition-colors cursor-pointer text-left"
    style={triggerFontStyle}
  >
    <span class="truncate">{triggerLabel}</span>
    <span class="material-symbols-outlined text-text-muted text-[18px] flex-shrink-0">
      {open ? 'expand_less' : 'expand_more'}
    </span>
  </button>

  {#if open}
    <div
      role="listbox"
      aria-label={label}
      tabindex="-1"
      class="absolute z-50 mt-1 w-full max-h-72 overflow-y-auto rounded-lg border border-border-active bg-bg-surface shadow-lg"
    >
      {#each options as o, i (o.group + '|' + o.value)}
        {#if o.group !== '' && (i === 0 || options[i - 1].group !== o.group)}
          <div
            class="px-3 pt-2 pb-1 text-text-muted text-[10px] font-label-sm-bold uppercase tracking-widest sticky top-0 bg-bg-surface"
          >
            {o.group}
          </div>
        {/if}
        <button
          type="button"
          role="option"
          aria-selected={i === selectedIndex}
          bind:this={optionEls[i]}
          onclick={() => commit(i)}
          onkeydown={(e) => onOptionKey(e, i)}
          style={o.cssFamily ? `font-family: ${o.cssFamily}` : ''}
          class="w-full text-left px-3 py-1.5 text-[13px] text-text-primary outline-none hover:bg-bg-hover focus:bg-bg-hover transition-colors cursor-pointer {i ===
          selectedIndex
            ? 'font-label-sm-bold'
            : 'font-body-md'}"
        >
          <span class="flex items-center gap-2">
            {o.label}
            {#if i === selectedIndex}
              <span class="material-symbols-outlined text-accent-primary-start text-[16px] ml-auto"
                >check</span
              >
            {/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}
</div>
