import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import path from 'node:path'
import os from 'node:os'

// Starts the *real* broker binary (../broker) against an in-memory fake
// Kubernetes backend (BROKER_FAKE_K8S=1 - see broker/cmd/broker/main.go),
// on the fixed port .env.test's VITE_BROKER_URL points api.ts at. This is
// what lets api.integration.test.ts assert against real broker HTTP
// responses instead of a hand-mocked fetch, which can silently drift from
// what the broker actually returns.
//
// Built to a temp binary and spawned directly rather than `go run ./...`:
// `go run` wraps the actual server in its own child process and doesn't
// reliably forward signals to it, so killing the `go run` process on
// teardown left the real broker orphaned and running - which then kept
// Vitest's process from exiting cleanly.
const PORT = 8879
const BROKER_DIR = path.resolve(import.meta.dirname, '../broker')
const HEALTHZ_URL = `http://localhost:${PORT}/healthz`

export default async function setup() {
  const binPath = path.join(os.tmpdir(), `marketplace-broker-fake-${process.pid}`)
  const build = spawnSync('go', ['build', '-o', binPath, './cmd/broker'], { cwd: BROKER_DIR, encoding: 'utf8' })
  if (build.status !== 0) {
    throw new Error(`building fake-backed broker failed:\n${build.stdout}\n${build.stderr}`)
  }

  const broker: ChildProcess = spawn(binPath, [], {
    env: {
      ...process.env,
      BROKER_FAKE_K8S: '1',
      BROKER_ADDR: `:${PORT}`,
      BROKER_CORS_ORIGIN: '',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  })

  let output = ''
  broker.stdout?.on('data', (chunk) => { output += chunk })
  broker.stderr?.on('data', (chunk) => { output += chunk })

  const exited = new Promise<never>((_, reject) => {
    broker.once('exit', (code) => reject(new Error(`fake-backed broker exited early (code ${code}):\n${output}`)))
  })

  await Promise.race([waitForHealthz(), exited])

  return async () => {
    broker.kill()
  }
}

async function waitForHealthz() {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    try {
      const res = await fetch(HEALTHZ_URL)
      if (res.ok) return
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 200))
  }
  throw new Error(`fake-backed broker never became healthy at ${HEALTHZ_URL} within 30s`)
}
