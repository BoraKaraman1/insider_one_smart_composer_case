import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const src = fileURLToPath(new URL('.', import.meta.url))

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? files(p) : [p]
  })
}

describe('templates', () => {
  it('never use v-html or innerHTML', () => {
    const offenders = files(src)
      .filter((f) => /\.(vue|ts|tsx|js)$/.test(f) && !f.endsWith('noVHtml.test.ts'))
      .filter((f) => /v-html|innerHTML|insertAdjacentHTML|outerHTML/.test(readFileSync(f, 'utf8')))
    expect(offenders).toEqual([])
  })

  it('finds the components it is guarding', () => {
    expect(files(src).some((f) => f.endsWith('.vue'))).toBe(true)
  })
})
