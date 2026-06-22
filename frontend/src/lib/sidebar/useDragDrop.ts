import { MovePage } from '../../../wailsjs/go/main/App.js'
import { sortByName, type NavOrderManager } from './navOrder'

export interface DragItem {
  level: string
  name: string
  section?: string
}

export interface DropTarget {
  level: string
  name: string
  before: boolean
}

export interface NavSection {
  name: string
  pages: { name: string }[]
  children?: NavSection[]
}

export interface DragDropDeps {
  getActiveNotebook: () => string
  getActiveNotebookSections: () => NavSection[]
  navOrder: NavOrderManager
  onDragItemChange: (item: DragItem | null) => void
  onDropTargetChange: (target: DropTarget | null) => void
  onError: (msg: string) => void
  onMoved: () => Promise<void>
  onPageMoved?: (
    notebook: string,
    fromSection: string,
    toSection: string,
    page: string
  ) => void
}

/**
 * Manages drag-and-drop logic for the sidebar: section reorder, page reorder,
 * and page→section cross-section moves.
 *
 * The component creates one instance and delegates all DnD events to it.
 */
export class DragDropManager {
  private dragItem: DragItem | null = null
  private dropTarget: DropTarget | null = null
  private deps: DragDropDeps

  constructor(deps: DragDropDeps) {
    this.deps = deps
  }

  handleDragStart(e: DragEvent, level: string, name: string, section?: string) {
    this.dragItem = { level, name, section }
    this.deps.onDragItemChange(this.dragItem)
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = 'move'
      e.dataTransfer.setData('text/plain', name)
    }
  }

  handleDragOver(e: DragEvent, level: string, name: string) {
    if (!this.dragItem) return
    // Same-level reorder (section↔section, page↔page) is always allowed.
    // Page→section drop (move into section, #177) is also allowed.
    if (
      this.dragItem.level !== level &&
      !(this.dragItem.level === 'page' && level === 'section')
    ) {
      return
    }
    e.preventDefault()
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
    const before = e.clientY < rect.top + rect.height / 2
    this.dropTarget = { level, name, before }
    this.deps.onDropTargetChange(this.dropTarget)
  }

  handleDragLeave() {
    this.dropTarget = null
    this.deps.onDropTargetChange(null)
  }

  async handleDrop(
    e: DragEvent,
    level: string,
    targetName: string,
    notebook?: string,
    section?: string
  ) {
    e.preventDefault()
    e.stopPropagation()
    if (!this.dragItem) {
      this.dropTarget = null
      this.deps.onDropTargetChange(null)
      return
    }

    const isPageToSection = this.dragItem.level === 'page' && level === 'section'
    if (this.dragItem.level !== level && !isPageToSection) {
      this.clear()
      return
    }
    if (this.dragItem.name === targetName && !isPageToSection) {
      this.clear()
      return
    }

    if (isPageToSection && section !== undefined) {
      // Page dropped onto a section header → cross-section move (#177).
      const fromSection = this.dragItem.section ?? ''
      const toSection = section
      if (fromSection === toSection) {
        this.clear()
        return
      }
      try {
        await MovePage(
          notebook ?? this.deps.getActiveNotebook(),
          fromSection,
          toSection,
          this.dragItem.name
        )
        await this.deps.onMoved()
        this.deps.onPageMoved?.(
          notebook ?? this.deps.getActiveNotebook(),
          fromSection,
          toSection,
          this.dragItem.name
        )
      } catch (err) {
        this.deps.onError(
          err instanceof Error ? err.message : 'Failed to move page'
        )
      }
    } else if (level === 'section' && notebook) {
      const sorted = sortByName(
        this.deps.getActiveNotebookSections(),
        this.deps.navOrder.current.sections[notebook]
      )
      const names = sorted.map((s) => s.name)
      const fromIdx = names.indexOf(this.dragItem.name)
      const toIdx = names.indexOf(targetName)
      if (fromIdx === -1 || toIdx === -1) {
        this.clear()
        return
      }
      names.splice(fromIdx, 1)
      const insertAt = this.dropTarget?.before
        ? names.indexOf(targetName)
        : names.indexOf(targetName) + 1
      names.splice(insertAt, 0, this.dragItem.name)
      await this.deps.navOrder.persistSectionOrder(notebook, names)
    } else if (level === 'page' && section) {
      const sec = this.deps
        .getActiveNotebookSections()
        .find((s) => s.name === section)
      const sectionKey = `${notebook ?? this.deps.getActiveNotebook()}/${section}`
      const sorted = sortByName(
        sec?.pages ?? [],
        this.deps.navOrder.current.pages[sectionKey]
      )
      const names = sorted.map((p) => p.name)
      const fromIdx = names.indexOf(this.dragItem.name)
      const toIdx = names.indexOf(targetName)
      if (fromIdx === -1 || toIdx === -1) {
        this.clear()
        return
      }
      names.splice(fromIdx, 1)
      const insertAt = this.dropTarget?.before
        ? names.indexOf(targetName)
        : names.indexOf(targetName) + 1
      names.splice(insertAt, 0, this.dragItem.name)
      await this.deps.navOrder.persistPageOrder(sectionKey, names)
    }

    this.clear()
  }

  handleDragEnd() {
    this.clear()
  }

  private clear() {
    this.dragItem = null
    this.dropTarget = null
    this.deps.onDragItemChange(null)
    this.deps.onDropTargetChange(null)
  }
}
