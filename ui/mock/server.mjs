#!/usr/bin/env node
// A tiny stand-in for the real broker (../../broker), for developing and
// demoing this UI without a live kind cluster. Implements the same HTTP
// contract described in the top-level README - same routes, same auth
// header, same JSON shapes - backed by an in-memory store instead of
// Kubernetes. Not used by the production build; dev-only.
import { createServer } from 'node:http'

const PORT = process.env.MOCK_BROKER_PORT ?? 8080

// Mirrors broker/config/teams.yaml.
const TEAMS = {
  'demo-key-payments': 'team-payments',
  'demo-key-checkout': 'team-checkout',
}

// Mirrors the catalog.Entry shape the real broker emits from installed
// Promises (broker/internal/catalog/catalog.go).
const PROMISES = [
  {
    name: 'database',
    displayName: 'Postgres Database',
    description: 'A sized, managed Postgres database, provisioned on request.',
    visible: true,
    group: 'demo.kratix.io',
    version: 'v1alpha1',
    kind: 'Database',
    plural: 'databases',
    scope: 'Namespaced',
    schema: {
      type: 'object',
      properties: {
        spec: {
          type: 'object',
          required: ['size'],
          properties: {
            size: {
              type: 'string',
              enum: ['1Gi', '5Gi', '10Gi', '50Gi'],
              description: 'Storage volume size for the database.',
            },
            highAvailability: {
              type: 'boolean',
              description: 'Run a standby replica for automatic failover.',
            },
          },
        },
      },
    },
  },
  {
    name: 'message-queue',
    displayName: 'Message Queue',
    description: 'A managed RabbitMQ cluster for async workloads between services.',
    visible: true,
    group: 'demo.kratix.io',
    version: 'v1alpha1',
    kind: 'MessageQueue',
    plural: 'messagequeues',
    scope: 'Namespaced',
    schema: {
      type: 'object',
      properties: {
        spec: {
          type: 'object',
          required: ['replicas'],
          properties: {
            replicas: { type: 'integer', minimum: 1, maximum: 5, description: 'Number of broker replicas.' },
            plan: { type: 'string', enum: ['standard', 'premium'], description: 'Throughput/durability tier.' },
            retention: {
              type: 'object',
              properties: {
                days: { type: 'integer', description: 'Days to retain unconsumed messages.' },
              },
            },
          },
        },
      },
    },
  },
  {
    name: 'redis-cache',
    displayName: 'Redis Cache',
    description: 'An in-memory cache cluster. Not yet published to the catalog.',
    visible: false,
    group: 'demo.kratix.io',
    version: 'v1alpha1',
    kind: 'Cache',
    plural: 'caches',
    scope: 'Namespaced',
    schema: {
      type: 'object',
      properties: {
        spec: { type: 'object', properties: { memoryMb: { type: 'integer', default: 256 } } },
      },
    },
  },
]

// requests[team][promiseName][requestName] = resource object
const requests = {}
for (const team of Object.values(TEAMS)) requests[team] = {}

function findPromise(name) {
  return PROMISES.find((p) => p.name === name)
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = ''
    req.on('data', (chunk) => (data += chunk))
    req.on('end', () => resolve(data ? JSON.parse(data) : {}))
    req.on('error', reject)
  })
}

function send(res, status, body) {
  res.writeHead(status, { 'Content-Type': 'application/json' })
  res.end(body === undefined ? undefined : JSON.stringify(body))
}

// New requests sit "Pending" for a bit, then flip to Ready (or, for names
// containing "fail", Failed) - so the UI's polling has something to show.
function simulateProgress(resource) {
  setTimeout(() => {
    resource.status = resource.metadata.name.includes('fail')
      ? {
          conditions: [
            { type: 'Ready', status: 'False', reason: 'ConfigureFailed', message: 'the pipeline exited non-zero' },
          ],
        }
      : {
          conditions: [{ type: 'Ready', status: 'True', reason: 'ConfigureComplete', message: 'ready to use' }],
        }
  }, 6000)
}

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`)
  const parts = url.pathname.split('/').filter(Boolean)

  if (req.method === 'GET' && url.pathname === '/healthz') {
    res.writeHead(200)
    res.end()
    return
  }

  // CORS: this mock is typically served from a different port than the
  // Vite dev server (5173).
  res.setHeader('Access-Control-Allow-Origin', '*')
  res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type')
  res.setHeader('Access-Control-Allow-Methods', 'GET, POST, DELETE, OPTIONS')
  if (req.method === 'OPTIONS') {
    res.writeHead(204)
    res.end()
    return
  }

  if (parts[0] !== 'api') return send(res, 404, { error: 'not found' })

  const auth = req.headers.authorization ?? ''
  const apiKey = auth.startsWith('Bearer ') ? auth.slice('Bearer '.length) : null
  const team = apiKey ? TEAMS[apiKey] : undefined
  if (!apiKey) return send(res, 401, { error: 'missing bearer token' })
  if (!team) return send(res, 401, { error: 'invalid API key' })

  const [, resource, name, sub, reqName] = parts

  try {
    if (resource === 'promises' && !name && req.method === 'GET') {
      const all = url.searchParams.get('all') === 'true'
      return send(
        res,
        200,
        PROMISES.filter((p) => all || p.visible),
      )
    }

    if (resource === 'promises' && name && !sub && req.method === 'GET') {
      const entry = findPromise(name)
      if (!entry) return send(res, 404, { error: `no such promise: ${name}` })
      return send(res, 200, entry)
    }

    if (resource === 'promises' && name && sub === 'requests' && !reqName) {
      const entry = findPromise(name)
      if (!entry) return send(res, 404, { error: `no such promise: ${name}` })

      if (req.method === 'GET') {
        return send(res, 200, Object.values(requests[team][name] ?? {}))
      }

      if (req.method === 'POST') {
        const body = await readBody(req)
        if (!body.name) return send(res, 400, { error: '"name" is required' })
        requests[team][name] ??= {}
        if (requests[team][name][body.name]) {
          return send(res, 409, { error: `a request named ${body.name} already exists` })
        }
        const resourceObj = {
          apiVersion: `${entry.group}/${entry.version}`,
          kind: entry.kind,
          metadata: { name: body.name, namespace: `team-${team}`, creationTimestamp: new Date().toISOString() },
          spec: body.spec ?? {},
          status: {},
        }
        requests[team][name][body.name] = resourceObj
        simulateProgress(resourceObj)
        return send(res, 201, resourceObj)
      }
    }

    if (resource === 'promises' && name && sub === 'requests' && reqName) {
      const entry = findPromise(name)
      if (!entry) return send(res, 404, { error: `no such promise: ${name}` })
      const bucket = requests[team][name] ?? {}

      if (req.method === 'GET') {
        if (!bucket[reqName]) return send(res, 404, { error: `no such request: ${reqName}` })
        return send(res, 200, bucket[reqName])
      }

      if (req.method === 'DELETE') {
        if (!bucket[reqName]) return send(res, 404, { error: `no such request: ${reqName}` })
        delete bucket[reqName]
        return send(res, 204)
      }
    }

    send(res, 404, { error: 'not found' })
  } catch (err) {
    send(res, 400, { error: err instanceof Error ? err.message : 'bad request' })
  }
})

server.listen(PORT, () => {
  console.log(`mock broker listening on http://localhost:${PORT}`)
  console.log('  demo-key-payments -> team-payments')
  console.log('  demo-key-checkout -> team-checkout')
})
