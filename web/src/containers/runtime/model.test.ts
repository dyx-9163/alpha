import { describe, expect, it } from 'vitest'

import {
  AIFAR_SERVICE_CONTROLLER_MODEL,
  isAifarServiceControllerModel
} from './model'

describe('AIFAR runtime orchestration model', () => {
  it('enables service-controller UI only for the migrated model', () => {
    expect(AIFAR_SERVICE_CONTROLLER_MODEL).toBe('agent-service-controller-v1')
    expect(isAifarServiceControllerModel('agent-service-controller-v1')).toBe(true)
    expect(isAifarServiceControllerModel('agent-runtime-v2')).toBe(false)
    expect(isAifarServiceControllerModel('')).toBe(false)
  })
})
