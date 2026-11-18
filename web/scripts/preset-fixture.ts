// Writes test/testdata/ui_presets.json: what every "New inbound" preset (src/lib/presets.ts) produces,
// for test/ui_presets_test.go to deploy and push traffic through. Placeholders (@CERT, @KEY,
// @CERT_ID, @PRIV, @PUB, @SID, port 0) are filled in by the test.
//   node --experimental-strip-types web/scripts/preset-fixture.ts          (writes the file)
//   node --experimental-strip-types web/scripts/preset-fixture.ts --check  (fails if it is stale)
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { PRESETS, type CertChoice } from '../src/lib/presets.ts'

const out = join(dirname(fileURLToPath(import.meta.url)), '../../test/testdata/ui_presets.json')
const keypair = async () => ({ private_key: '@PRIV', public_key: '@PUB', short_id: '@SID' })
const randomKey = () => 'dGVzdHRlc3R0ZXN0dGVzdA=='
const sources: CertChoice[] = [
  { src: 'path', certId: '', certPath: '@CERT', keyPath: '@KEY', email: '' },
  { src: 'panel', certId: '@CERT_ID', certPath: '', keyPath: '', email: '' },
  { src: 'acme', certId: '', certPath: '', keyPath: '', email: 'admin@sb.test' },
]
const cases = []
for (const p of PRESETS) {
  for (const c of p.tls ? sources : [sources[0]]) {
    const r = await p.build({ port: 0, sni: p.tls ? 'sb.test' : 'target.test', handshake: '', path: p.path === 'grpc' ? 'grpc' : '/ws', ...c }, keypair, randomKey)
    cases.push({ id: p.id, cert: p.tls ? c.src : '', type: p.type, udp: p.udp, options: r.options, client: r.client })
  }
}
const text = JSON.stringify(cases, null, 2) + '\n'
if (process.argv.includes('--check')) {
  let cur = ''
  try { cur = readFileSync(out, 'utf8') } catch { /* missing */ }
  if (cur !== text) { console.error(`${out} is stale: run node --experimental-strip-types web/scripts/preset-fixture.ts`); process.exit(1) }
} else {
  writeFileSync(out, text)
  console.log(`wrote ${cases.length} cases to ${out}`)
}
