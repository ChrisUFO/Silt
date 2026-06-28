import type { RegisteredPlugin } from './sdk'
import Agenda from './first-party/silt-agenda/Agenda.svelte'
import Calendar from './first-party/silt-calendar/Calendar.svelte'
import CalendarSidebar from './first-party/silt-calendar/CalendarSidebar.svelte'
import Kanban from './first-party/silt-kanban/Kanban.svelte'
import KanbanSidebar from './first-party/silt-kanban/KanbanSidebar.svelte'
import AttachmentsPlugin from './first-party/silt-attachments'

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
    author: 'Silt',
    description: 'Rolling agenda of overdue, today, and upcoming tasks.',
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
    author: 'Silt',
    description:
      'Month, week, and agenda layouts of tasks by due date. Smart-list sidebar (#322).',
    icon: 'calendar_month'
  },
  component: Calendar,
  sidebarComponent: CalendarSidebar,
  source: 'first-party'
})
registerPlugin({
  manifest: {
    id: 'silt-kanban',
    name: 'Kanban',
    version: '1.0.0',
    author: 'Silt',
    description: 'Drag-and-drop task board (TODO / DOING / DONE).',
    icon: 'view_kanban',
    settings: [
      {
        key: 'default_col',
        label: 'Default Column',
        type: 'select',
        options: ['TODO', 'DOING', 'DONE'],
        default: 'TODO'
      }
    ]
  },
  component: Kanban,
  sidebarComponent: KanbanSidebar,
  source: 'first-party'
})
// silt-attachments (#101): attaches files to notes via /attach. The plugin
// module exports its component + onVaultOpen hook (which registers the slash
// command). Unlike Agenda/Calendar/Kanban, this plugin uses the v2 SDK
// lifecycle hooks + slash-command registry.
registerPlugin({
  manifest: AttachmentsPlugin.manifest,
  component: AttachmentsPlugin.component,
  onVaultOpen: AttachmentsPlugin.onVaultOpen,
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
