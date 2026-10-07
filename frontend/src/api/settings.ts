// 全局设置：主题模式（跟随系统/浅色/深色）+ 终端外观。localStorage 持久化。
import { reactive, watch } from 'vue'

export interface TerminalTheme {
  background: string; foreground: string; cursor: string
  selectionBackground: string
}

export const TERMINAL_THEMES: Record<string, TerminalTheme> = {
  dark:      { background: '#0c0c0c', foreground: '#e5e5e5', cursor: '#ffffff', selectionBackground: '#3a3a3a' },
  light:     { background: '#ffffff', foreground: '#24292f', cursor: '#24292f', selectionBackground: '#add6ff' },
  solarized: { background: '#002b36', foreground: '#839496', cursor: '#93a1a1', selectionBackground: '#073642' },
  dracula:   { background: '#282a36', foreground: '#f8f8f2', cursor: '#f8f8f2', selectionBackground: '#44475a' },
}

export const FONT_FAMILIES = [
  { label: '等宽默认', value: "'JetBrains Mono', 'Cascadia Code', Menlo, monospace" },
  { label: 'Consolas', value: "Consolas, 'Courier New', monospace" },
  { label: 'Monaco', value: "Monaco, Menlo, monospace" },
  { label: '系统 Monospace', value: 'monospace' },
]

export interface Settings {
  themeMode: 'system' | 'light' | 'dark'
  terminal: {
    fontSize: number
    fontFamily: string
    theme: string
    background: string   // 空 = 跟随主题
    autoSyncSize: boolean  // 窗口变化时自动注入 stty 同步尺寸（串口无尺寸协商）
  }
}

const DEFAULTS: Settings = {
  themeMode: 'system',
  terminal: { fontSize: 14, fontFamily: FONT_FAMILIES[0].value, theme: 'dark', background: '', autoSyncSize: false },
}

function load(): Settings {
  try {
    const raw = localStorage.getItem('linkhub_settings')
    if (raw) return { ...DEFAULTS, ...JSON.parse(raw), terminal: { ...DEFAULTS.terminal, ...(JSON.parse(raw).terminal ?? {}) } }
  } catch { /* 坏数据用默认 */ }
  return structuredClone(DEFAULTS)
}

export const settings = reactive<Settings>(load())

watch(settings, v => localStorage.setItem('linkhub_settings', JSON.stringify(v)), { deep: true })

// ---------- 主题应用 ----------
const media = window.matchMedia('(prefers-color-scheme: dark)')

function isDark(): boolean {
  if (settings.themeMode === 'system') return media.matches
  return settings.themeMode === 'dark'
}

export function applyTheme() {
  document.documentElement.classList.toggle('dark', isDark())
}

media.addEventListener('change', applyTheme)
watch(() => settings.themeMode, applyTheme)
applyTheme()

export function terminalThemeOf(): TerminalTheme & { background: string } {
  const t = TERMINAL_THEMES[settings.terminal.theme] ?? TERMINAL_THEMES.dark
  return { ...t, background: settings.terminal.background || t.background }
}
