import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const toolDir = path.dirname(fileURLToPath(import.meta.url))
const repositoryRoot = path.resolve(toolDir, '..', '..')
const workflowPath = path.join(
  repositoryRoot,
  '.github',
  'workflows',
  'aifar-bundle-packager-release.yml'
)

test('release workflow is tag-only, versioned, tested, and draft-only', () => {
  const workflow = readFileSync(workflowPath, 'utf8')

  assert.match(workflow, /tags:[\s\S]*aifar-bundle-packager-v\*/)
  assert.doesNotMatch(workflow, /pull_request:/)
  assert.match(workflow, /contents:\s*write/)
  assert.match(workflow, /runs-on:\s*windows-latest/)
  assert.match(workflow, /timeout-minutes:\s*30/)
  assert.match(workflow, /actions\/checkout@v4[\s\S]*fetch-depth:\s*0/)
  assert.match(workflow, /actions\/setup-node@v4[\s\S]*node-version:\s*22/)
  assert.match(workflow, /actions\/setup-dotnet@v4[\s\S]*dotnet-version:\s*8\.0\.x/)
  assert.match(workflow, /pnpm@11\.7\.0/)
  assert.match(workflow, /pnpm test:tools/)
  assert.match(workflow, /pnpm test:scripts/)
  assert.match(workflow, /dotnet test tools\/aifar-bundle-packager\/AifarBundlePackager\.sln/)
  assert.match(workflow, /build\.ps1[\s\S]*-Version[\s\S]*-SourceRevisionId/)
  assert.match(workflow, /create-release-assets\.ps1/)
  assert.match(workflow, /gh release create[\s\S]*--draft[\s\S]*--verify-tag/)
  assert.doesNotMatch(workflow, /gh release create[^\r\n]*--target/)
})

test('release workflow verifies tag provenance, Git safety, and exact assets', () => {
  const workflow = readFileSync(workflowPath, 'utf8')

  assert.match(workflow, /\^aifar-bundle-packager-v\(\\d\+\\\.\\d\+\\\.\\d\+\)\$/)
  assert.match(workflow, /git rev-parse[\s\S]*github\.sha/i)
  assert.match(workflow, /git ls-files[\s\S]*deploy\/bin\/AIFARBundlePackager\.exe/i)
  assert.match(workflow, /100000000/)
  assert.match(workflow, /Get-FileHash[\s\S]*SHA256/i)
  assert.match(workflow, /ConvertFrom-Json/)
  assert.match(workflow, /AIFARBundlePackager\.exe\.sha256/)
  assert.match(workflow, /release-manifest\.json/)
  assert.match(workflow, /Git commit/i)
  assert.match(workflow, /win-x64/i)
  assert.match(workflow, /Java-only/i)
  assert.match(workflow, /Web-only/i)
  assert.match(workflow, /mixed/i)
  assert.match(workflow, /GH_TOKEN:\s*\$\{\{ github\.token \}\}/)
})
