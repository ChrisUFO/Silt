// Curated color palette for text/background color pickers (#170).
// Each entry has dark-mode and light-mode hex variants so colored text is
// legible against either theme background.

export interface ColorEntry {
  id: string
  label: string
  dark: string
  light: string
}

export const DEFAULT_COLOR_PALETTE: ColorEntry[] = [
  { id: 'red',     label: 'Red',     dark: '#f87171', light: '#dc2626' },
  { id: 'orange',  label: 'Orange',  dark: '#fb923c', light: '#ea580c' },
  { id: 'yellow',  label: 'Yellow',  dark: '#facc15', light: '#ca8a04' },
  { id: 'green',   label: 'Green',   dark: '#4ade80', light: '#16a34a' },
  { id: 'teal',    label: 'Teal',    dark: '#2dd4bf', light: '#0d9488' },
  { id: 'blue',    label: 'Blue',    dark: '#60a5fa', light: '#2563eb' },
  { id: 'indigo',  label: 'Indigo',  dark: '#818cf8', light: '#4f46e5' },
  { id: 'purple',  label: 'Purple',  dark: '#c084fc', light: '#9333ea' },
  { id: 'pink',    label: 'Pink',    dark: '#f472b6', light: '#db2777' },
  { id: 'brown',   label: 'Brown',   dark: '#a8a29e', light: '#78350f' },
  { id: 'gray',    label: 'Gray',    dark: '#a1a1aa', light: '#52525b' },
  { id: 'black',   label: 'Black',   dark: '#fafafa', light: '#18181b' }
]

/**
 * Resolve the hex value for the current theme mode.
 * @param entry palette entry
 * @param isDark true if dark mode is active
 */
export function resolveColor(entry: ColorEntry, isDark: boolean): string {
  return isDark ? entry.dark : entry.light
}
