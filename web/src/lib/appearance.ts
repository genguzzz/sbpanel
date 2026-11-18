// Panel appearance: palette, light/dark mode, custom accent, radius, density.
// Sources (last wins): server default (window.__SBP_UI, injected into index.html) -> this browser (localStorage 'sbp-ui').
// The inline boot script in index.html applies the same state before first paint; keep the two in sync.
import { useSyncExternalStore } from 'react'
import { tr } from '../i18n'
import { P } from '../i18n/appearance'

export type Mode = 'system' | 'light' | 'dark'
export type Palette = 'neutral' | 'rosepine' | 'catppuccin' | 'nord'
export type Radius = 'default' | 'sharp' | 'round'
export type Density = 'default' | 'compact'
export type AccentSet = { accent: string; accentText: string; onAccent: string }
export type Appearance = {
  mode?: Mode; palette?: Palette; radius?: Radius; density?: Density
  accent?: { light: AccentSet; dark: AccentSet; source?: string }
}

export const PALETTES: { id: Palette; label: string; hint: string; swatch: { light: string[]; dark: string[] } }[] = [
  { id: 'neutral', get label() { return tr(P).neutral }, get hint() { return tr(P).neutralHint }, swatch: { light: ['#f5f6f8', '#ffffff', '#1f6a88', '#1b1d22'], dark: ['#111215', '#1a1c20', '#5aaed0', '#eceef2'] } },
  { id: 'rosepine', label: 'Rosé Pine', get hint() { return tr(P).rosepineHint }, swatch: { light: ['#faf4ed', '#fffaf3', '#286983', '#3c3858'], dark: ['#191724', '#232136', '#5ba3c4', '#e0def4'] } },
  { id: 'catppuccin', label: 'Catppuccin', hint: 'Latte / Mocha', swatch: { light: ['#e6e9ef', '#eff1f5', '#1857d6', '#4c4f69'], dark: ['#11111b', '#1e1e2e', '#89b4fa', '#cdd6f4'] } },
  { id: 'nord', label: 'Nord', get hint() { return tr(P).nordHint }, swatch: { light: ['#e5e9f0', '#f3f5f8', '#36608c', '#2e3440'], dark: ['#1f232b', '#2b303b', '#88c0d0', '#eceff4'] } },
]

// Surfaces + text per palette/mode, used to resolve a custom accent that stays readable everywhere.
const SURFACES: Record<Palette, Record<'light' | 'dark', { bgs: string[]; text: string }>> = {
  neutral: { light: { bgs: ['#f5f6f8', '#ffffff', '#eceef2'], text: '#1b1d22' }, dark: { bgs: ['#111215', '#1a1c20', '#25272d', '#2c2f36'], text: '#eceef2' } },
  rosepine: { light: { bgs: ['#faf4ed', '#fffaf3', '#f2e9e1'], text: '#3c3858' }, dark: { bgs: ['#191724', '#232136', '#2f2c46', '#393552'], text: '#e0def4' } },
  catppuccin: { light: { bgs: ['#e6e9ef', '#eff1f5', '#dce0e8'], text: '#4c4f69' }, dark: { bgs: ['#11111b', '#1e1e2e', '#2a2b3c', '#313244'], text: '#cdd6f4' } },
  nord: { light: { bgs: ['#e5e9f0', '#f3f5f8', '#d8dee9'], text: '#2e3440' }, dark: { bgs: ['#1f232b', '#2b303b', '#353b48', '#3b4252'], text: '#eceff4' } },
}

export const ACCENT_PRESETS = ['#1f6a88', '#2563eb', '#7c3aed', '#db2777', '#dc2626', '#ea580c', '#ca8a04', '#16a34a', '#0d9488']

// ---- colour maths (WCAG 2.x) ----
const rgb = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16))
const lin = (v: number) => { v /= 255; return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4 }
const lum = (h: string) => { const [r, g, b] = rgb(h).map(lin); return 0.2126 * r + 0.7152 * g + 0.0722 * b }
export const contrast = (a: string, b: string) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05) }
const mix = (a: string, b: string, t: number) => { const A = rgb(a), B = rgb(b); return '#' + A.map((v, i) => Math.round(v + (B[i] - v) * t).toString(16).padStart(2, '0')).join('') }
export const isHex = (s: string) => /^#[0-9a-f]{6}$/i.test(s)

/** One value that reads as text on every surface (>= 4.5:1) and carries its ink (>= 4.5:1) as a button fill. */
export function resolveAccent(hex: string, palette: Palette): { light: AccentSet; dark: AccentSet } {
  const one = (mode: 'light' | 'dark'): AccentSet => {
    const { bgs, text } = SURFACES[palette][mode]
    let a = hex.toLowerCase()
    for (let i = 0; i < 30 && Math.min(...bgs.map((b) => contrast(a, b))) < 4.5; i++) a = mix(a, text, 0.07)
    const ink = mode === 'dark' ? '#0b0d10' : '#ffffff'
    for (let i = 0; i < 30 && contrast(a, ink) < 4.5; i++) a = mix(a, mode === 'dark' ? '#ffffff' : '#000000', 0.06)
    return { accent: a, accentText: a, onAccent: ink }
  }
  return { light: one('light'), dark: one('dark') }
}

// ---- state ----
const KEY = 'sbp-ui'
declare global { interface Window { __SBP_UI?: Appearance } }

function readLocal(): Appearance | null {
  try {
    const own = localStorage.getItem(KEY)
    if (own) return JSON.parse(own)
    const legacy = localStorage.getItem('sbp-theme')
    if (legacy === 'light' || legacy === 'dark') return { mode: legacy }
  } catch { /* storage blocked */ }
  return null
}

let serverDefault: Appearance = (typeof window !== 'undefined' && window.__SBP_UI) || {}
let local: Appearance | null = readLocal()
let snapshot: Appearance = { ...serverDefault, ...(local ?? {}) }
const subs = new Set<() => void>()

function recompute() {
  snapshot = { ...serverDefault, ...(local ?? {}) }
  apply(snapshot)
  subs.forEach((f) => f())
}

export function apply(a: Appearance) {
  const d = document.documentElement
  if (a.mode === 'light' || a.mode === 'dark') d.dataset.theme = a.mode
  else delete d.dataset.theme
  d.dataset.palette = a.palette || 'neutral'
  if (a.radius && a.radius !== 'default') d.dataset.radius = a.radius
  else delete d.dataset.radius
  if (a.density && a.density !== 'default') d.dataset.density = a.density
  else delete d.dataset.density
  document.getElementById('sbp-accent')?.remove()
  // Only plain hex colours reach the stylesheet (the values come from localStorage / the server).
  const okSet = (x?: AccentSet) => !!x && isHex(x.accent) && isHex(x.accentText) && isHex(x.onAccent)
  if (a.accent && okSet(a.accent.light) && okSet(a.accent.dark)) {
    const v = (x: AccentSet) => `--accent:${x.accent};--accent-text:${x.accentText};--on-accent:${x.onAccent};--focus:${x.accentText};--pine:${x.accent};`
    const s = document.createElement('style')
    s.id = 'sbp-accent'
    s.textContent = `:root:root:root:root{${v(a.accent.light)}}@media (prefers-color-scheme: dark){:root:root:root:root:not([data-theme="light"]){${v(a.accent.dark)}}}:root:root:root:root[data-theme="dark"]{${v(a.accent.dark)}}`
    document.head.appendChild(s)
  }
  // Charts and other canvas consumers re-read CSS variables on this event.
  window.dispatchEvent(new CustomEvent('sbp:appearance', { detail: { ...a, dark: isDark(a) } }))
}

export function isDark(a: Appearance = snapshot) {
  return a.mode ? a.mode === 'dark' : window.matchMedia('(prefers-color-scheme: dark)').matches
}

/** Updates this browser's choice (merged over the server default). */
export function setLocal(patch: Partial<Appearance>) {
  local = { ...(local ?? {}), ...patch }
  for (const k of Object.keys(local) as (keyof Appearance)[]) if (local[k] === undefined) delete local[k]
  try { localStorage.setItem(KEY, JSON.stringify(local)); localStorage.removeItem('sbp-theme') } catch { /* storage blocked */ }
  recompute()
}

/** Forget this browser's choices and follow the server default again. */
export function clearLocal() {
  local = null
  try { localStorage.removeItem(KEY); localStorage.removeItem('sbp-theme') } catch { /* storage blocked */ }
  recompute()
}

export function setServerDefault(a: Appearance) { serverDefault = a; recompute() }
export const hasLocal = () => local != null && Object.keys(local).length > 0

function subscribe(f: () => void) {
  subs.add(f)
  const mq = window.matchMedia('(prefers-color-scheme: dark)')
  const on = () => { if (!snapshot.mode || snapshot.mode === 'system') { snapshot = { ...snapshot }; apply(snapshot); f() } }
  mq.addEventListener('change', on)
  return () => { subs.delete(f); mq.removeEventListener('change', on) }
}

export function useAppearance() {
  const a = useSyncExternalStore(subscribe, () => snapshot)
  return { appearance: a, dark: isDark(a), server: serverDefault, hasLocal: hasLocal() }
}
