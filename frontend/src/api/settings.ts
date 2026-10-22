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
  stylePreset: StylePresetKey
  language: 'system' | 'zh' | 'en'
  terminal: {
    fontSize: number
    fontFamily: string
    theme: string
    background: string   // 空 = 跟随主题
    autoSyncSize: boolean  // 窗口变化时自动注入 stty 同步尺寸（串口无尺寸协商）
    toolbarVisible: boolean // 终端功能区（工具栏）显示
    scrollback: number    // 终端回滚历史行数
  }
}

// ---------- 界面风格预设 ----------
// 每套预设是一组 CSS 变量：侧栏/工作台/面板/强调色全部由它驱动，保证各区域同一风格
export type StylePresetKey =
  | 'ocean' | 'forest' | 'sunset' | 'graphite'
  | 'ocean-light' | 'forest-light' | 'sunset-light' | 'graphite-light'
  | 'daylight'

export interface StylePreset {
  label: string
  dark: boolean
  vars: Record<string, string>
}

export const STYLE_PRESETS: Record<StylePresetKey, StylePreset> = {
  ocean: { label: '深海蓝', dark: true, vars: {
    '--lh-sidebar-bg': '#0b1c30', '--lh-sidebar-active': '#14304e', '--lh-sidebar-fg': '#9fb3c8',
    '--lh-accent': '#3b8bfd', '--lh-workbench-bg': '#0c1116', '--lh-panel-bg': '#131920',
    '--lh-panel-border': '#222b34', '--lh-fg': '#dce3ea', '--lh-muted': '#8a94a0' } },
  forest: { label: '松林绿', dark: true, vars: {
    '--lh-sidebar-bg': '#0d211a', '--lh-sidebar-active': '#14382b', '--lh-sidebar-fg': '#93b8a7',
    '--lh-accent': '#2ea36b', '--lh-workbench-bg': '#0b100e', '--lh-panel-bg': '#121a16',
    '--lh-panel-border': '#213329', '--lh-fg': '#dbe7e0', '--lh-muted': '#7fa391' } },
  sunset: { label: '暖沙橙', dark: true, vars: {
    '--lh-sidebar-bg': '#241812', '--lh-sidebar-active': '#3a271c', '--lh-sidebar-fg': '#c9a990',
    '--lh-accent': '#e08a3c', '--lh-workbench-bg': '#121009', '--lh-panel-bg': '#1d1712',
    '--lh-panel-border': '#34291e', '--lh-fg': '#ece2d8', '--lh-muted': '#a8927f' } },
  graphite: { label: '石墨青', dark: true, vars: {
    '--lh-sidebar-bg': '#16181b', '--lh-sidebar-active': '#23272d', '--lh-sidebar-fg': '#9aa1a9',
    '--lh-accent': '#39b3a6', '--lh-workbench-bg': '#0e1012', '--lh-panel-bg': '#17191d',
    '--lh-panel-border': '#272b31', '--lh-fg': '#d9dde2', '--lh-muted': '#8b929a' } },
  'ocean-light': { label: '深海蓝 · 亮', dark: false, vars: {
    '--lh-sidebar-bg': '#eaf1f8', '--lh-sidebar-active': '#d7e5f7', '--lh-sidebar-fg': '#43566b',
    '--lh-accent': '#1f6feb', '--lh-workbench-bg': '#ffffff', '--lh-panel-bg': '#f2f5f9',
    '--lh-panel-border': '#dfe5ec', '--lh-fg': '#2b3138', '--lh-muted': '#7a8694' } },
  'forest-light': { label: '松林绿 · 亮', dark: false, vars: {
    '--lh-sidebar-bg': '#e9f4ee', '--lh-sidebar-active': '#d4e9dd', '--lh-sidebar-fg': '#3f5c4e',
    '--lh-accent': '#1f8f5a', '--lh-workbench-bg': '#ffffff', '--lh-panel-bg': '#eef6f1',
    '--lh-panel-border': '#d9e7de', '--lh-fg': '#243329', '--lh-muted': '#6f8a7c' } },
  'sunset-light': { label: '暖沙橙 · 亮', dark: false, vars: {
    '--lh-sidebar-bg': '#fbf1e8', '--lh-sidebar-active': '#f6e2ce', '--lh-sidebar-fg': '#6b5744',
    '--lh-accent': '#d97a2b', '--lh-workbench-bg': '#ffffff', '--lh-panel-bg': '#faf3ea',
    '--lh-panel-border': '#eedfd0', '--lh-fg': '#3a2f26', '--lh-muted': '#a08b76' } },
  'graphite-light': { label: '石墨青 · 亮', dark: false, vars: {
    '--lh-sidebar-bg': '#f2f3f5', '--lh-sidebar-active': '#e4e8ec', '--lh-sidebar-fg': '#4c545d',
    '--lh-accent': '#2b8f85', '--lh-workbench-bg': '#ffffff', '--lh-panel-bg': '#f2f3f5',
    '--lh-panel-border': '#e1e4e8', '--lh-fg': '#2c3036', '--lh-muted': '#7c838c' } },
  daylight: { label: '清晨亮', dark: false, vars: {
    '--lh-sidebar-bg': '#f4f6f9', '--lh-sidebar-active': '#e8eefb', '--lh-sidebar-fg': '#5a6472',
    '--lh-accent': '#1f6feb', '--lh-workbench-bg': '#ffffff', '--lh-panel-bg': '#f4f6f9',
    '--lh-panel-border': '#e2e6ec', '--lh-fg': '#2b2f36', '--lh-muted': '#7a828c' } },
}

const DEFAULTS: Settings = {
  themeMode: 'dark',
  stylePreset: 'ocean',
  language: 'system',
  terminal: { fontSize: 14, fontFamily: FONT_FAMILIES[0].value, theme: 'dark', background: '', autoSyncSize: false, toolbarVisible: true, scrollback: 20000 },
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
  return STYLE_PRESETS[settings.stylePreset]?.dark ?? true
}

export function applyTheme() {
  const root = document.documentElement
  root.classList.toggle('dark', isDark())
  const preset = STYLE_PRESETS[settings.stylePreset] ?? STYLE_PRESETS.ocean
  for (const [k, v] of Object.entries(preset.vars)) root.style.setProperty(k, v)
}

// setStylePreset 切换风格（同步 themeMode 供旧逻辑/组件参考）
export function setStylePreset(key: StylePresetKey) {
  settings.stylePreset = key
  settings.themeMode = STYLE_PRESETS[key]?.dark ? 'dark' : 'light'
  applyTheme()
}

media.addEventListener('change', applyTheme)
watch(() => settings.themeMode, applyTheme)
applyTheme()

export function terminalThemeOf(): TerminalTheme & { background: string } {
  const t = TERMINAL_THEMES[settings.terminal.theme] ?? TERMINAL_THEMES.dark
  return { ...t, background: settings.terminal.background || t.background }
}
