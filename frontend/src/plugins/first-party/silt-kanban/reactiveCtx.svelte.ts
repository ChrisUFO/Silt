// Test helper: a $state-backed navigation holder so Kanban's reactive reads of
// ctx.activeNotebook/Section/Page track changes the test makes (Svelte only
// tracks reads of $state/$derived signals, not plain closure variables). Used
// by the #124 auto-narrow tests to simulate in-app navigation without a full
// remount or real IPC.
//
// This file MUST be .svelte.ts so the `$state` rune compiles.

import type { PluginContext } from '../../sdk'
import { v2CtxStubs } from '../../test-helpers'

interface NavState {
  notebook: string
  section: string
  page: string
}

export const navState = $state<NavState>({
  notebook: 'Work',
  section: '',
  page: ''
})

export function setNav(next: Partial<NavState>) {
  navState.notebook = next.notebook ?? navState.notebook
  navState.section = next.section ?? navState.section
  navState.page = next.page ?? navState.page
}

export function resetNav() {
  navState.notebook = 'Work'
  navState.section = ''
  navState.page = ''
}

// Build a PluginContext whose active nav getters are backed by navState, so
// the Kanban board reacts to setNav() calls. sqliteQuery is injected so each
// test controls its own query mock.
export function reactiveCtx(
  sqliteQuery: PluginContext['sqliteQuery'],
  extras: Partial<PluginContext> = {}
): PluginContext {
  return {
    get activeNotebook() {
      return navState.notebook
    },
    get activeSection() {
      return navState.section
    },
    get activePage() {
      return navState.page
    },
    get today() {
      return extras.today ?? '2026-06-16'
    },
    sqliteQuery,
    updateBlockState: extras.updateBlockState ?? (() => Promise.resolve(true)),
    mutateBlock: extras.mutateBlock ?? (() => Promise.resolve(true)),
    updateTaskMeta: extras.updateTaskMeta ?? (() => Promise.resolve(true)),
    getPluginSettings: extras.getPluginSettings ?? (() => Promise.resolve({})),
    on: extras.on ?? (() => () => {}),
    ...v2CtxStubs
  }
}
