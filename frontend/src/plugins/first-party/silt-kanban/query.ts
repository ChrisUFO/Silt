import { plusDaysISO } from '../../sdk'
import type { KanbanFilters, Scope } from './types'

/**
 * The shape `buildQuery` reads from the PluginContext. Pass an explicit
 * narrow object so the query builder is obviously pure and unit-testable
 * without instantiating a real PluginContext.
 */
export interface QueryCtxLike {
  activeNotebook: string
  activeSection: string
  activePage: string
  today: string
}

/**
 * Build a `b.id IN (?, ?, ...)` placeholder list for an arbitrary number of
 * values. Used so the owner and priority WHERE-fragments share one shape.
 */
function inClause(column: string, values: unknown[]): string {
  return `${column} IN (${values.map(() => '?').join(', ')})`
}

/**
 * Build the parameterised SQL for the Kanban board query.
 *
 * Two phases:
 *  1. Scope WHERE — narrows by vault / notebook / section / page using the
 *     active navigation triple.
 *  2. Filter WHERE — owners (IN), priorities (IN), due-date quick-pick
 *     (overdue / today / week / none), and tags (subquery on the `tags`
 *     table). All bound via `?` params — never string-interpolated.
 *
 * Due dates compare against the LOCAL day (ctxLike.today), not SQLite's
 * date('now') which is UTC and produced off-by-one results near local
 * midnight (#118). due_date is stored as YYYY-MM-DD text, so lexicographic
 * comparison against the param matches the old date('now') semantics exactly.
 *
 * `t.pinned` is a tri-state cache column (#135) — NULL/0/1 for
 * absent/false/true. The board treats only `1` as pinned.
 */
export function buildQuery(
  s: Scope,
  f: KanbanFilters,
  ctx: QueryCtxLike
): { sql: string; params: unknown[] } {
  const baseSelect = `SELECT b.id, b.notebook, b.section, b.page, b.file_date, b.line_number,
           b.clean_content, t.status, t.owner, t.start_date, t.due_date, t.priority,
           t.pinned, t.progress, t.comments_count, t.links_count,
           (SELECT GROUP_CONCAT(raw_path, '|') FROM tags WHERE block_id = b.id) AS tags
    FROM blocks b JOIN tasks t ON b.id = t.block_id`
  const orderBy = ` ORDER BY t.priority ASC, COALESCE(t.due_date, '9999-12-31') ASC`
  const where: string[] = []
  const params: unknown[] = []
  switch (s) {
    case 'vault':
      break
    case 'notebook':
      where.push('b.notebook = ?')
      params.push(ctx.activeNotebook)
      break
    case 'section':
      where.push('b.notebook = ?', 'b.section = ?')
      params.push(ctx.activeNotebook, ctx.activeSection)
      break
    case 'page':
      where.push('b.notebook = ?', 'b.section = ?', 'b.page = ?')
      params.push(ctx.activeNotebook, ctx.activeSection, ctx.activePage)
      break
  }
  if (f.owners.length) {
    where.push(inClause('t.owner', f.owners))
    params.push(...f.owners)
  }
  if (f.priorities.length) {
    where.push(inClause('t.priority', f.priorities))
    params.push(...f.priorities)
  }
  if (f.dueDate) {
    const today = ctx.today
    if (f.dueDate === 'overdue') {
      where.push('t.due_date < ?')
      params.push(today)
    } else if (f.dueDate === 'today') {
      where.push('t.due_date = ?')
      params.push(today)
    } else if (f.dueDate === 'week') {
      where.push('t.due_date BETWEEN ? AND ?')
      params.push(today, plusDaysISO(today, 7))
    } else if (f.dueDate === 'none') {
      where.push("(t.due_date IS NULL OR t.due_date = '')")
    }
  }
  if (f.tags.length) {
    where.push(
      `b.id IN (SELECT block_id FROM tags WHERE raw_path IN (${f.tags
        .map(() => '?')
        .join(', ')}))`
    )
    params.push(...f.tags)
  }
  const whereClause = where.length
    ? ' WHERE ' + where.join(' AND ')
    : ' WHERE 1=1'
  return { sql: baseSelect + whereClause + orderBy, params }
}
