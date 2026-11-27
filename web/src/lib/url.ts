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

/** The project's source repository. */
export const REPO_URL = 'https://github.com/genguzzz/sbpanel'

/** A "new issue" link on GitHub, prefilled with the panel version and browser so reports arrive with context. */
export function issueUrl(version?: string): string {
  const body = [
    '### Describe the problem', '', '', '### Steps to reproduce', '', '1. ', '',
    '### Environment', '',
    `- sbpanel: ${version || 'unknown'}`,
    `- Browser: ${navigator.userAgent}`,
  ].join('\n')
  return `${REPO_URL}/issues/new?${new URLSearchParams({ body })}`
}
