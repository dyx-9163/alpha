import { describe, expect, it } from 'vitest'

import { parseRuntimeLogLine, parseRuntimeLogLines } from './logs'

describe('runtime log parsing', () => {
  it('parses Spring timestamps and bracketed error levels', () => {
    expect(parseRuntimeLogLine('2026-07-25 10:20:30.123 [ERROR] gateway failed')).toMatchObject({
      time: '2026-07-25 10:20:30.123',
      level: 'ERROR',
      message: 'gateway failed'
    })
  })

  it('parses structured JSON logs', () => {
    expect(parseRuntimeLogLine('{"timestamp":"2026-07-25T10:20:30Z","level":"error","message":"database unavailable"}')).toMatchObject({
      time: '2026-07-25T10:20:30Z',
      level: 'ERROR',
      message: 'database unavailable'
    })
  })

  it('normalizes warning and fatal aliases', () => {
    expect(parseRuntimeLogLine('2026-07-25T10:20:30Z WARNING capacity low').level).toBe('WARN')
    expect(parseRuntimeLogLine('2026-07-25T10:20:30Z FATAL process stopped').level).toBe('ERROR')
  })

  it('keeps a complete exception stack in error context', () => {
    const result = parseRuntimeLogLines([
      '2026-07-25 10:20:30.123 ERROR request failed',
      'java.lang.IllegalStateException: unavailable',
      '  at com.aifar.Service.run(Service.java:42)',
      'Suppressed: java.io.IOException: closed',
      'Caused by: java.net.ConnectException: refused',
      '  ... 3 more',
      '2026-07-25 10:20:31.000 INFO recovered'
    ])

    expect(result.lines.map((line) => line.level)).toEqual([
      'ERROR', 'ERROR', 'ERROR', 'ERROR', 'ERROR', 'ERROR', 'INFO'
    ])
    expect(result.context.error).toBe(false)
  })

  it('does not leak error context into unrelated unclassified output', () => {
    const result = parseRuntimeLogLines([
      '2026-07-25T10:20:30Z ERROR request failed',
      'java.lang.IllegalArgumentException: invalid request',
      'ordinary application output'
    ])

    expect(result.lines.map((line) => line.level)).toEqual(['ERROR', 'ERROR', ''])
    expect(result.context.error).toBe(false)
  })
})
