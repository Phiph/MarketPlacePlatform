# Third-Party Licenses

This project is licensed under the [Apache License 2.0](LICENSE). It also
redistributes Kubernetes manifests rendered from the Helm charts of the
third-party projects below, checked into this repository as Promise
dependencies. Their original copyright and license terms are preserved here.

## Zalando Postgres Operator

- **Used in:** [promises/database/workflows/promise/configure/dependencies/configure-deps/resources/operator.yaml](promises/database/workflows/promise/configure/dependencies/configure-deps/resources/operator.yaml)
- **Source:** https://github.com/zalando/postgres-operator
- **Chart version:** 1.8.2
- **License:** MIT

```
The MIT License (MIT)

Copyright (c) 2026 Zalando SE

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Capsule

- **Used in:**
  - [promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/operator.yaml](promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/operator.yaml)
  - [promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/capsuleconfiguration.crd.yaml](promises/business-unit/workflows/promise/configure/dependencies/configure-deps/resources/capsuleconfiguration.crd.yaml)
- **Source:** https://github.com/clastix/capsule
- **Chart version:** 0.13.11
- **License:** Apache License 2.0 (same license as this project — see [LICENSE](LICENSE) for full text)

Copyright the Capsule Authors ([clastix.io](https://clastix.io)). Licensed
under the Apache License, Version 2.0; you may not use these files except
in compliance with the License. You may obtain a copy of the License at
http://www.apache.org/licenses/LICENSE-2.0.

## Kratix

Kratix itself (the platform this repo builds Promises for) is not vendored
into this repository — it is installed separately as a Kubernetes operator
and interacted with via the Kubernetes API (`client-go`, CRDs). It is
licensed under the Apache License 2.0: https://github.com/syntasso/kratix
