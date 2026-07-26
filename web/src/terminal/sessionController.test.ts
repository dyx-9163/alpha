import { describe, expect, it, vi } from 'vitest'

import { createTerminalSessionController, type TerminalSocketLike } from './sessionController'

function fakeSocket(): TerminalSocketLike {
  return {
    binaryType: 'blob',
    readyState: 0,
    onopen: null,
    onmessage: null,
    onerror: null,
    onclose: null,
    send: vi.fn(),
    close: vi.fn()
  }
}

function controllerFor(socket: TerminalSocketLike, write = vi.fn(), onState = vi.fn()) {
  return {
    controller: createTerminalSessionController({
      terminal: { write },
      createSocket: () => socket,
      onState,
      connectionFailedText: 'failed',
      disconnectedText: 'closed'
    }),
    write,
    onState
  }
}

describe('terminal session controller', () => {
  it('routes output to only the terminal that owns the socket', () => {
    const firstSocket = fakeSocket()
    const secondSocket = fakeSocket()
    const first = controllerFor(firstSocket)
    const second = controllerFor(secondSocket)
    first.controller.connect()
    second.controller.connect()

    firstSocket.onmessage?.({ data: 'first' } as MessageEvent)

    expect(first.write).toHaveBeenCalledWith('first')
    expect(second.write).not.toHaveBeenCalled()
  })

  it('keeps error state when a browser follows onerror with onclose', () => {
    const socket = fakeSocket()
    const { controller, write, onState } = controllerFor(socket)
    controller.connect()

    socket.onerror?.({} as Event)
    socket.onclose?.({} as CloseEvent)

    expect(write).toHaveBeenCalledWith('\r\nfailed\r\n')
    expect(onState).toHaveBeenLastCalledWith('error')
  })

  it('manual disconnect closes the current socket once and changes state', () => {
    const socket = fakeSocket()
    const { controller, onState } = controllerFor(socket)
    controller.connect()

    controller.disconnect()
    controller.dispose()

    expect(socket.close).toHaveBeenCalledTimes(1)
    expect(onState).toHaveBeenLastCalledWith('disconnected')
  })

  it('ignores late socket messages after disposal', () => {
    const socket = fakeSocket()
    const { controller, write } = controllerFor(socket)
    controller.connect()

    const staleMessage = socket.onmessage
    controller.dispose()
    staleMessage?.({ data: 'late' } as MessageEvent)

    expect(write).not.toHaveBeenCalled()
    expect(socket.close).toHaveBeenCalledTimes(1)
  })

  it('does not write a Blob that resolves after disposal', async () => {
    const socket = fakeSocket()
    const { controller, write } = controllerFor(socket)
    let resolveBuffer!: (value: ArrayBuffer) => void
    const payload = new Blob(['late'])
    vi.spyOn(payload, 'arrayBuffer').mockImplementation(
      () => new Promise<ArrayBuffer>((resolve) => { resolveBuffer = resolve })
    )
    controller.connect()

    socket.onmessage?.({ data: payload } as MessageEvent)
    controller.dispose()
    resolveBuffer(new ArrayBuffer(4))
    await Promise.resolve()

    expect(write).not.toHaveBeenCalled()
  })

  it('reconnect closes the previous socket and ignores its callbacks', () => {
    const firstSocket = fakeSocket()
    const secondSocket = fakeSocket()
    const sockets = [firstSocket, secondSocket]
    const write = vi.fn()
    const onState = vi.fn()
    const controller = createTerminalSessionController({
      terminal: { write },
      createSocket: () => sockets.shift()!,
      onState,
      connectionFailedText: 'failed',
      disconnectedText: 'closed'
    })
    controller.connect()
    const staleMessage = firstSocket.onmessage

    controller.connect()
    staleMessage?.({ data: 'old' } as MessageEvent)
    secondSocket.onmessage?.({ data: 'new' } as MessageEvent)

    expect(firstSocket.close).toHaveBeenCalledTimes(1)
    expect(write).toHaveBeenCalledTimes(1)
    expect(write).toHaveBeenCalledWith('new')
  })
})
