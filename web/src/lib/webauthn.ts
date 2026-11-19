import { api } from './api'

/**
 * Passkey (WebAuthn) ceremonies against /api/me/passkeys and /api/auth/passkey.
 * The server sends PublicKeyCredential*Options JSON with binary fields as base64url; this
 * converts them to ArrayBuffers for navigator.credentials and the result back to JSON, without
 * relying on PublicKeyCredential.parseCreationOptionsFromJSON (not in every browser yet).
 */

export type Passkey = { id: string; name: string; created_at: number; last_used_at: number }

type Begin = { session: string; options: any }

export const passkeysSupported = () => typeof window !== 'undefined' && !!window.PublicKeyCredential && !!navigator.credentials

export function b64urlToBuf(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, '+').replace(/_/g, '/') + '==='.slice((s.length + 3) % 4)
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out.buffer
}

export function bufToB64url(b: ArrayBuffer | ArrayBufferView): string {
  const bytes = b instanceof ArrayBuffer ? new Uint8Array(b) : new Uint8Array(b.buffer, b.byteOffset, b.byteLength)
  let bin = ''
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i])
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

const descriptors = (list?: { id: string; type: string; transports?: string[] }[]) =>
  list?.map((c) => ({ ...c, id: b64urlToBuf(c.id) })) as PublicKeyCredentialDescriptor[] | undefined

function creationOptions(o: any): PublicKeyCredentialCreationOptions {
  return { ...o, challenge: b64urlToBuf(o.challenge), user: { ...o.user, id: b64urlToBuf(o.user.id) }, excludeCredentials: descriptors(o.excludeCredentials) ?? [] }
}

function requestOptions(o: any): PublicKeyCredentialRequestOptions {
  return { ...o, challenge: b64urlToBuf(o.challenge), allowCredentials: descriptors(o.allowCredentials) ?? [] }
}

/** The JSON form of a credential, as the server's parser expects it. */
function credentialJSON(c: PublicKeyCredential) {
  const r = c.response as AuthenticatorAttestationResponse & AuthenticatorAssertionResponse
  const response: Record<string, unknown> = { clientDataJSON: bufToB64url(r.clientDataJSON) }
  if ('attestationObject' in r && r.attestationObject) {
    response.attestationObject = bufToB64url(r.attestationObject)
    if (typeof r.getTransports === 'function') response.transports = r.getTransports()
  }
  if ('authenticatorData' in r && r.authenticatorData) {
    response.authenticatorData = bufToB64url(r.authenticatorData)
    response.signature = bufToB64url(r.signature)
    if (r.userHandle) response.userHandle = bufToB64url(r.userHandle)
  }
  return {
    id: c.id, rawId: bufToB64url(c.rawId), type: c.type, response,
    authenticatorAttachment: c.authenticatorAttachment ?? undefined,
    clientExtensionResults: c.getClientExtensionResults(),
  }
}

/** Creates a passkey on this device and saves it under name. */
export async function addPasskey(name: string): Promise<Passkey> {
  const b = await api<Begin>('/me/passkeys/register/begin', { method: 'POST' })
  const cred = await navigator.credentials.create({ publicKey: creationOptions(b.options) })
  if (!cred) throw new Error('No credential')
  return api<Passkey>('/me/passkeys/register/finish', { method: 'POST', body: { session: b.session, name, credential: credentialJSON(cred as PublicKeyCredential) } })
}

/** Signs in with a discoverable passkey; resolves with the admin's username. */
export async function signInWithPasskey(): Promise<{ username: string }> {
  const b = await api<Begin>('/auth/passkey/begin', { method: 'POST' })
  const cred = await navigator.credentials.get({ publicKey: requestOptions(b.options) })
  if (!cred) throw new Error('No credential')
  return api<{ username: string }>('/auth/passkey/finish', { method: 'POST', body: { session: b.session, credential: credentialJSON(cred as PublicKeyCredential) } })
}

/** True when the browser refused or the user dismissed the prompt (not worth an error message). */
export const isPasskeyCancel = (e: unknown) => e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')

/** A default name for a new passkey: the platform (e.g. "macOS"), else "Passkey". */
export function defaultPasskeyName(): string {
  const p = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform
  return p?.trim() || 'Passkey'
}
