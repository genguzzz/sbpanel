import { useState } from 'react'
import { Form } from 'react-aria-components'
import { api, ApiError } from '../lib/api'
import { isPasskeyCancel, passkeysSupported, signInWithPasskey } from '../lib/webauthn'
import { Button, Field, LangSwitch } from '../ui/kit'
import { useT } from '../i18n'
import { SH } from '../i18n/shell'

export function Login({ onDone }: { onDone: () => void }) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [needCode, setNeedCode] = useState(false)
  const [code, setCode] = useState('')
  const [passkeyBusy, setPasskeyBusy] = useState(false)
  const t = useT(SH)
  const passkey = async () => {
    setPasskeyBusy(true)
    setErr('')
    try {
      await signInWithPasskey()
      onDone()
    } catch (e) {
      if (!isPasskeyCancel(e)) setErr((e as Error).message)
    } finally {
      setPasskeyBusy(false)
    }
  }
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api('/auth/login', { method: 'POST', body: { username, password, code } })
      onDone()
    } catch (e) {
      if (e instanceof ApiError && e.data?.need_totp) {
        if (needCode) setErr((e as Error).message)
        setNeedCode(true)
      } else setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="relative grid min-h-full place-items-center p-4">
      <div className="absolute top-3 right-3"><LangSwitch /></div>
      <Form onSubmit={submit} className="w-full max-w-[380px] rounded-[20px] border bg-surface p-8 animate-pop">
        <div className="mb-6 flex items-center gap-3">
          <span className="grid h-10 w-10 place-items-center rounded-[12px] bg-accent text-on-accent">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round"><path d="M5 16c3-9 11-9 14 0" /><circle cx="12" cy="17" r="1.6" fill="currentColor" /></svg>
          </span>
          <div>
            <div className="text-lg font-semibold">sbpanel</div>
            <div className="text-[13px] text-subtle">{t.tagline}</div>
          </div>
        </div>
        <div className="flex flex-col gap-4">
          <Field label={t.username} value={username} onChange={setUsername} autoComplete="username" isRequired />
          <Field label={t.password} type="password" value={password} onChange={setPassword} autoComplete="current-password" autoFocus isRequired />
          {needCode && <Field label={t.totp} value={code} onChange={setCode} autoComplete="one-time-code" autoFocus description={t.totpHint} />}
          {err && <div className="rounded-[10px] bg-bad/10 px-3 py-2 text-[13px] text-bad">{err}</div>}
          <Button type="submit" variant="primary" isPending={busy} className="mt-1 h-10">{busy ? t.signingIn : t.signIn}</Button>
          {passkeysSupported() && (
            <>
              <div className="flex items-center gap-3 text-[12px] text-subtle"><span className="h-px flex-1 bg-hl-med" />{t.or}<span className="h-px flex-1 bg-hl-med" /></div>
              <Button isPending={passkeyBusy} isDisabled={busy} onPress={passkey} className="h-10">{t.passkeySignIn}</Button>
            </>
          )}
        </div>
      </Form>
    </div>
  )
}
