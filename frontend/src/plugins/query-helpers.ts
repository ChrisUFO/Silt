// Shared SQL query-building helpers for plugins (#104). These DRY up the
// common tag/date/scope WHERE-clause patterns that the Kanban and future
// plugins would otherwise hand-write. Each helper returns a complete
// { sql, params } pair ready to pass to ctx.sqliteQuery.

export type QueryScope = 'vault' | 'notebook' | 'section' | 'page'

export interface ScopeFilter {
  notebook?: string
  section?: string
  page?: string
}

/**
 * Build the base blocks+tasks SELECT with optional scope narrowing. Plugins
 * compose on top of this by appending their own WHERE conditions.
 */
export function baseTaskQuery(
  scope: QueryScope,
  filter: ScopeFilter
): { sql: string; params: unknown[] } {
  const baseSelect = `SELECT b.id, b.notebook, b.section, b.page, b.file_date, b.line_number,
       b.clean_content, t.status, t.owner, t.start_date, t.due_date, t.priority,
       t.pinned, t.progress, t.comments_count, t.links_count,
       (SELECT GROUP_CONCAT(raw_path, '|') FROM tags WHERE block_id = b.id) AS tags
  FROM blocks b JOIN tasks t ON b.id = t.block_id`
  const where: string[] = []
  const params: unknown[] = []
  switch (scope) {
    case 'notebook':
      if (filter.notebook) {
        where.push('b.notebook = ?')
        params.push(filter.notebook)
      }
      break
    case 'section':
      if (filter.notebook) {
        where.push('b.notebook = ?')
        params.push(filter.notebook)
      }
      if (filter.section) {
        where.push('b.section = ?')
        params.push(filter.section)
      }
      break
    case 'page':
      if (filter.notebook) {
        where.push('b.notebook = ?')
        params.push(filter.notebook)
      }
      if (filter.section) {
        where.push('b.section = ?')
        params.push(filter.section)
      }
      if (filter.page) {
        where.push('b.page = ?')
        params.push(filter.page)
      }
      break
    // 'vault' = no scope narrowing
  }
  const whereClause = where.length > 0 ? ' WHERE ' + where.join(' AND ') : ''
  return { sql: baseSelect + whereClause, params }
}

/**
 * Build a tag-path WHERE clause fragment. Returns { clause, param } where
 * clause is a condition to AND into a query, and param is the tag path (with
 * a `/%` suffix for hierarchical matching).
 */
export function tagWhereClause(column = 'b.id'): {
  clause: string
  param: (path: string) => string
} {
  return {
    clause: `EXISTS (SELECT 1 FROM tags t WHERE t.block_id = ${column} AND (t.raw_path = ? OR t.raw_path LIKE ?))`,
    param: (path: string) => path + '/%'
  }
}
