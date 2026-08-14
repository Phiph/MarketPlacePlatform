#!/usr/bin/env node
// A tiny stand-in for the real broker (../../broker), for developing and
// demoing this UI without a live kind cluster. Implements the same HTTP
// contract described in the top-level README - same routes, same auth
// header, same JSON shapes - backed by an in-memory store instead of
// Kubernetes. Not used by the production build; dev-only.
import { createServer } from 'node:http'

const PORT = process.env.MOCK_BROKER_PORT ?? 8878

// Mirrors broker/config/teams.yaml.
const TEAMS = {
  'demo-key-payments': 'payments',
  'demo-key-checkout': 'checkout',
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
  // Mirrors promises/project/promise.yaml. Not self-served through the
  // generic catalog (visible: false) - the UI's Projects page hits these
  // same generic /promises/project/requests routes directly by name.
  {
    name: 'project',
    displayName: 'Project',
    description: 'A logical grouping of environments owned by one team.',
    visible: false,
    group: 'demo.kratix.io',
    version: 'v1alpha1',
    kind: 'Project',
    plural: 'projects',
    scope: 'Namespaced',
    schema: {
      type: 'object',
      properties: {
        spec: {
          type: 'object',
          properties: {
            description: { type: 'string', description: 'Optional human-readable description of this project.' },
          },
        },
      },
    },
  },
  // Mirrors promises/environment/promise.yaml. Creation always goes
  // through POST /environments below, never this generic route - see that
  // handler for why.
  {
    name: 'environment',
    displayName: 'Environment',
    description: 'Provisions one environment (dev/staging/prod/...) of a Project as its own namespace.',
    visible: false,
    group: 'demo.kratix.io',
    version: 'v1alpha1',
    kind: 'Environment',
    plural: 'environments',
    scope: 'Namespaced',
    schema: {
      type: 'object',
      properties: {
        spec: {
          type: 'object',
          required: ['project', 'team', 'businessUnit'],
          properties: {
            project: { type: 'string' },
            team: { type: 'string' },
            businessUnit: { type: 'string' },
          },
        },
      },
    },
  },
]

// Mirrors broker/config/teams.yaml's businessUnits grouping - needed to
// compose an Environment's spec.businessUnit the same way the real
// broker's POST /environments does.
const BUSINESS_UNIT_BY_TEAM = { payments: 'platform-org', checkout: 'platform-org' }

// requests[team][promiseName][requestName] = resource object
const requests = {}
for (const team of Object.values(TEAMS)) requests[team] = {}

// Scoped equivalent of `requests` above, for the
// /projects/{project}/environments/{environment}/promises/{name}/requests
// routes:
// scopedRequests[team][project][environment][promiseName][requestName]
const scopedRequests = {}
for (const team of Object.values(TEAMS)) scopedRequests[team] = {}

// boundVersions[team][promiseName][requestName] = version string. Tracks
// which PromiseRevision each request is pinned to, mirroring the real
// broker's ResourceBinding.spec.version (broker/internal/bindingapi). A
// request with no entry here is treated as bound to its Promise's latest
// version (same default the real broker's bindingapi.Version applies to a
// binding with no version set).
const boundVersions = {}
for (const team of Object.values(TEAMS)) boundVersions[team] = {}

// Scoped equivalent of `boundVersions` above, mirroring scopedRequests:
// scopedBoundVersions[team][project][environment][promiseName][requestName]
const scopedBoundVersions = {}
for (const team of Object.values(TEAMS)) scopedBoundVersions[team] = {}

function findPromise(name) {
  return PROMISES.find((p) => p.name === name)
}

// Mirrors broker/cmd/broker/fake_seed.go's seeded PromiseRevisions: the
// database Promise has two revisions on record (v0.1.0, and v0.2.0 marked
// latest). Every other Promise gets one synthetic "latest" revision so the
// version-upgrade feature still works generically against the mock, not
// just for database.
const VERSIONS = {
  database: [
    { version: 'v0.1.0', latest: false, createdAt: '2026-07-01T00:00:00Z' },
    { version: 'v0.2.0', latest: true, createdAt: '2026-08-10T00:00:00Z' },
  ],
}

function versionsFor(promiseName) {
  return VERSIONS[promiseName] ?? [{ version: 'v1.0.0', latest: true, createdAt: '2026-01-01T00:00:00Z' }]
}

function latestVersionOf(versions) {
  return versions.find((v) => v.latest)?.version ?? ''
}

// Builds the RequestVersionInfo shape shared by the GET and POST
// .../version routes (ui/src/lib/types.ts's RequestVersionInfo, mirroring
// broker/internal/api/server.go's requestVersionInfo).
function requestVersionInfo(bound, versions) {
  const latest = latestVersionOf(versions)
  return { boundVersion: bound, latestVersion: latest, upgradeAvailable: bound !== latest }
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

  const [, resource, name, sub, reqName, versionSeg] = parts

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

    if (resource === 'promises' && name && sub === 'versions' && !reqName && req.method === 'GET') {
      const entry = findPromise(name)
      if (!entry) return send(res, 404, { error: `no such promise: ${name}` })
      return send(res, 200, versionsFor(name))
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

    if (resource === 'promises' && name && sub === 'requests' && reqName && versionSeg === 'version') {
      const entry = findPromise(name)
      if (!entry) return send(res, 404, { error: `no such promise: ${name}` })
      const bucket = requests[team][name] ?? {}
      const versions = versionsFor(name)

      if (req.method === 'GET') {
        if (!bucket[reqName]) return send(res, 404, { error: `no such request: ${reqName}` })
        const bound = boundVersions[team][name]?.[reqName] ?? latestVersionOf(versions)
        return send(res, 200, requestVersionInfo(bound, versions))
      }

      if (req.method === 'POST') {
        const body = await readBody(req)
        if (!versions.some((v) => v.version === body.version)) {
          return send(res, 404, { error: `no such promise version: ${body.version}` })
        }
        if (!bucket[reqName]) return send(res, 404, { error: `no such request: ${reqName}` })
        boundVersions[team][name] ??= {}
        boundVersions[team][name][reqName] = body.version
        return send(res, 200, requestVersionInfo(body.version, versions))
      }
    }

    if (resource === 'promises' && name && sub === 'requests' && reqName && !versionSeg) {
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

    // POST /api/environments - the one dedicated endpoint, mirroring
    // broker/internal/api/server.go's createEnvironment: composes
    // spec.team/spec.businessUnit itself rather than trusting the request
    // body, since those become RBAC-relevant on the real broker (see
    // promises/environment/README.md). The mock has no real RBAC to
    // protect, but keeps the same contract so the UI can't tell the
    // difference.
    if (resource === 'environments' && !name && req.method === 'POST') {
      const body = await readBody(req)
      if (!body.name) return send(res, 400, { error: '"name" is required' })
      if (!body.project) return send(res, 400, { error: '"project" is required' })

      if (!requests[team].project?.[body.project]) {
        return send(res, 404, { error: `no such project: ${body.project}` })
      }

      requests[team].environment ??= {}
      if (requests[team].environment[body.name]) {
        return send(res, 409, { error: `a request named ${body.name} already exists` })
      }

      const entry = findPromise('environment')
      const resourceObj = {
        apiVersion: `${entry.group}/${entry.version}`,
        kind: entry.kind,
        metadata: { name: body.name, namespace: `team-${team}`, creationTimestamp: new Date().toISOString() },
        spec: { project: body.project, team, businessUnit: BUSINESS_UNIT_BY_TEAM[team] },
        // team is part of the namespace name, not just project/environment -
        // see tenant.ProjectEnvironmentNamespace()'s doc comment on the real
        // broker: two teams naming a project+environment identically would
        // otherwise collide on one real Namespace.
        status: { namespace: `project-${team}-${body.project}-${body.name}` },
      }
      requests[team].environment[body.name] = resourceObj
      simulateProgress(resourceObj)
      return send(res, 201, resourceObj)
    }

    // /api/projects/{project}/environments/{environment}/promises/{name}/requests[/{reqName}]
    // - scoped equivalent of the flat /api/promises/{name}/requests routes
    // above, landing in a project's environment namespace instead of the
    // team's flat default one. No ownership check on {project}/{environment}
    // here, mirroring the real broker's server.go comment: it's Kubernetes
    // RBAC that enforces that boundary for real, not a broker-side string
    // comparison - the mock just has nothing to enforce it with, so any
    // authenticated team can address any project/environment pair here.
    if (resource === 'projects') {
      const project = parts[2]
      const environment = parts[3] === 'environments' ? parts[4] : undefined
      const promiseName = parts[5] === 'promises' ? parts[6] : undefined
      const scopedReqName = parts[7] === 'requests' ? parts[8] : undefined
      const scopedVersionSeg = parts[9]

      if (!project || !environment || !promiseName || parts[7] !== 'requests') {
        return send(res, 404, { error: 'not found' })
      }

      const entry = findPromise(promiseName)
      if (!entry) return send(res, 404, { error: `no such promise: ${promiseName}` })

      scopedRequests[team][project] ??= {}
      scopedRequests[team][project][environment] ??= {}
      scopedRequests[team][project][environment][promiseName] ??= {}
      const bucket = scopedRequests[team][project][environment][promiseName]

      if (!scopedReqName) {
        if (req.method === 'GET') return send(res, 200, Object.values(bucket))

        if (req.method === 'POST') {
          const body = await readBody(req)
          if (!body.name) return send(res, 400, { error: '"name" is required' })
          if (bucket[body.name]) return send(res, 409, { error: `a request named ${body.name} already exists` })
          const resourceObj = {
            apiVersion: `${entry.group}/${entry.version}`,
            kind: entry.kind,
            metadata: {
              name: body.name,
              namespace: `project-${team}-${project}-${environment}`,
              creationTimestamp: new Date().toISOString(),
            },
            spec: body.spec ?? {},
            status: {},
          }
          bucket[body.name] = resourceObj
          simulateProgress(resourceObj)
          return send(res, 201, resourceObj)
        }
      } else if (scopedVersionSeg === 'version') {
        const versions = versionsFor(promiseName)

        if (req.method === 'GET') {
          if (!bucket[scopedReqName]) return send(res, 404, { error: `no such request: ${scopedReqName}` })
          const bound =
            scopedBoundVersions[team][project]?.[environment]?.[promiseName]?.[scopedReqName] ??
            latestVersionOf(versions)
          return send(res, 200, requestVersionInfo(bound, versions))
        }

        if (req.method === 'POST') {
          const body = await readBody(req)
          if (!versions.some((v) => v.version === body.version)) {
            return send(res, 404, { error: `no such promise version: ${body.version}` })
          }
          if (!bucket[scopedReqName]) return send(res, 404, { error: `no such request: ${scopedReqName}` })
          scopedBoundVersions[team][project] ??= {}
          scopedBoundVersions[team][project][environment] ??= {}
          scopedBoundVersions[team][project][environment][promiseName] ??= {}
          scopedBoundVersions[team][project][environment][promiseName][scopedReqName] = body.version
          return send(res, 200, requestVersionInfo(body.version, versions))
        }
      } else {
        if (req.method === 'GET') {
          if (!bucket[scopedReqName]) return send(res, 404, { error: `no such request: ${scopedReqName}` })
          return send(res, 200, bucket[scopedReqName])
        }

        if (req.method === 'DELETE') {
          if (!bucket[scopedReqName]) return send(res, 404, { error: `no such request: ${scopedReqName}` })
          delete bucket[scopedReqName]
          return send(res, 204)
        }
      }
    }

    send(res, 404, { error: 'not found' })
  } catch (err) {
    send(res, 400, { error: err instanceof Error ? err.message : 'bad request' })
  }
})

server.listen(PORT, () => {
  console.log(`mock broker listening on http://localhost:${PORT}`)
  console.log('  demo-key-payments -> payments')
  console.log('  demo-key-checkout -> checkout')
})
