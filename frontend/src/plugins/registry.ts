import type { RegisteredPlugin } from './sdk'
import Agenda from './first-party/silt-agenda/Agenda.svelte'
import Calendar from './first-party/silt-calendar/Calendar.svelte'

// First-party plugin registry: bundled Svelte components that ship with the
// app. Third-party plugins live in .system/plugins/ and are loaded by the
// loader; both go through the identical PluginContext SDK.
const registry = new Map<string, RegisteredPlugin>()

// Register built-in plugins. Agenda (#17) and Calendar (#18) are built
// exclusively on the PluginContext SDK, exactly as a third-party plugin would.
registerPlugin({
  manifest: {
    id: 'silt-agenda',
    name: 'Agenda',
    version: '1.0.0',
    icon: 'event_repeat'
  },
  component: Agenda,
  source: 'first-party'
})
registerPlugin({
  manifest: {
    id: 'silt-calendar',
    name: 'Calendar',
    version: '1.0.0',
    icon: 'calendar_month'
  },
  component: Calendar,
  source: 'first-party'
})

export function registerPlugin(plugin: RegisteredPlugin): void {
  registry.set(plugin.manifest.id, plugin)
}

export function getFirstParty(id: string): RegisteredPlugin | undefined {
  return registry.get(id)
}

export function firstPartyPlugins(): RegisteredPlugin[] {
  return [...registry.values()]
}
