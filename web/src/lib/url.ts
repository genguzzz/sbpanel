/** `u` when it is an absolute http(s) URL, else undefined: for links whose target comes from data. */
export function httpUrl(u: string | null | undefined): string | undefined {
  if (!u) return undefined
  try {
    const p = new URL(u)
    return p.protocol === 'https:' || p.protocol === 'http:' ? p.href : undefined
  } catch {
    return undefined
  }
}
