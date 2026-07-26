import type { TerminalConnectionState } from './sessions'

export interface TerminalSocketLike {
  binaryType: BinaryType
  readonly readyState: number
  onopen: ((event: Event) => any) | null
  onmessage: ((event: MessageEvent) => any) | null
  onerror: ((event: Event) => any) | null
  onclose: ((event: CloseEvent) => any) | null
  send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void
  close(code?: number, reason?: string): void
}

interface TerminalSessionControllerOptions {
  terminal: { write(data: string | Uint8Array): void }
  createSocket(): TerminalSocketLike
  onState(state: TerminalConnectionState): void
  connectionFailedText: string
  disconnectedText: string
}

export function createTerminalSessionController(options: TerminalSessionControllerOptions) {
  let socket: TerminalSocketLike | null = null
  let generation = 0
  let disposed = false
  let currentState: TerminalConnectionState = 'disconnected'

  function setState(state: TerminalConnectionState) {
    currentState = state
    options.onState(state)
  }

  function writeData(data: unknown, expectedGeneration: number) {
    if (data instanceof ArrayBuffer) {
      if (!disposed && generation === expectedGeneration) {
        options.terminal.write(new Uint8Array(data))
      }
      return
    }
    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => {
        if (!disposed && generation === expectedGeneration) {
          options.terminal.write(new Uint8Array(buffer))
        }
      })
      return
    }
    if (!disposed && generation === expectedGeneration) {
      options.terminal.write(String(data))
    }
  }

  function closeCurrent() {
    const current = socket
    socket = null
    if (!current) return
    current.onopen = null
    current.onmessage = null
    current.onerror = null
    current.onclose = null
    current.close()
  }

  function connect() {
    if (disposed) return
    generation += 1
    closeCurrent()
    const expectedGeneration = generation
    setState('connecting')
    const current = options.createSocket()
    socket = current
    current.binaryType = 'arraybuffer'
    current.onopen = () => {
      if (!disposed && generation === expectedGeneration) setState('connected')
    }
    current.onmessage = (event) => writeData(event.data, expectedGeneration)
    current.onerror = () => {
      if (disposed || generation !== expectedGeneration) return
      options.terminal.write(`\r\n${options.connectionFailedText}\r\n`)
      setState('error')
    }
    current.onclose = () => {
      if (disposed || generation !== expectedGeneration || currentState === 'error') return
      options.terminal.write(`\r\n${options.disconnectedText}\r\n`)
      setState('disconnected')
    }
  }

  function disconnect() {
    if (disposed) return
    generation += 1
    closeCurrent()
    setState('disconnected')
  }

  function send(data: string) {
    if (socket?.readyState === 1) socket.send(data)
  }

  function dispose() {
    if (disposed) return
    disposed = true
    generation += 1
    closeCurrent()
  }

  return { connect, disconnect, send, dispose }
}
