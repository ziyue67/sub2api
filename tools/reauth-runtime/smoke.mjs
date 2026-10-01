import assert from 'node:assert/strict'
import http from 'node:http'
import { TlsFingerprintTransport } from '/runtime/tosub2/src/tls-transport.mjs'
const server = http.createServer((_req, res) => {
  res.setHeader('Content-Type', 'application/json')
  res.end('{"ok":true}')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const transport = new TlsFingerprintTransport({ enabled: true, profile: 'chrome146' })
try {
  const response = await transport.request('GET', 'http://127.0.0.1:' + server.address().port + '/')
  assert.equal(response.status, 200)
  assert.deepEqual(await response.json(), { ok: true })
  console.log('portable Node -> Python -> curl_cffi transport passed')
} finally {
  await transport.close()
  await new Promise(resolve => server.close(resolve))
}
