// One shared websocket to the PaperPerps server, multiplexing topic subscriptions.
// Reconnects with backoff and resubscribes; the server answers each subscribe with a
// snapshot, so state heals itself after a reconnect.

export type ServerMessage<T = unknown> = {
  topic: string
  snapshot?: boolean
  data: T
}

export type Handler = (msg: ServerMessage) => void

export type ConnectionState = 'connecting' | 'open' | 'closed'

export class MarketSocket {
  private ws: WebSocket | null = null
  private readonly handlers = new Map<string, Set<Handler>>()
  // Extra fields sent with a topic's subscribe request, e.g. the token for account topics.
  private readonly params = new Map<string, Record<string, string>>()
  private state: ConnectionState = 'closed'
  private readonly listeners = new Set<() => void>()
  private retries = 0
  private retryTimer: ReturnType<typeof setTimeout> | undefined
  private readonly url: string

  constructor(url: string) {
    this.url = url
  }

  /** Subscribe handler to topic. Returns a function that undoes it. */
  subscribe(topic: string, handler: Handler, params?: Record<string, string>): () => void {
    // Wrap so the same function can be subscribed twice and unsubscribed independently.
    const h: Handler = (msg) => handler(msg)
    let set = this.handlers.get(topic)
    if (!set) {
      set = new Set()
      this.handlers.set(topic, set)
      if (params) this.params.set(topic, params)
      this.send({ op: 'subscribe', topic, ...params })
    }
    set.add(h)
    this.connect()

    return () => {
      const current = this.handlers.get(topic)
      if (!current) return
      current.delete(h)
      if (current.size === 0) {
        this.handlers.delete(topic)
        this.params.delete(topic)
        this.send({ op: 'unsubscribe', topic })
      }
    }
  }

  getState = (): ConnectionState => this.state

  onStateChange = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  private setState(state: ConnectionState) {
    this.state = state
    for (const l of this.listeners) l()
  }

  private send(msg: { op: string; topic: string; [key: string]: string }) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(msg))
  }

  private connect() {
    if (this.ws || this.retryTimer) return
    this.setState('connecting')
    const ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = () => {
      this.retries = 0
      this.setState('open')
      for (const topic of this.handlers.keys()) {
        this.send({ op: 'subscribe', topic, ...this.params.get(topic) })
      }
    }

    ws.onmessage = (ev: MessageEvent<string>) => {
      let msg: ServerMessage
      try {
        msg = JSON.parse(ev.data) as ServerMessage
      } catch {
        return
      }
      if (msg.topic === 'error') {
        console.warn('websocket error reply', msg.data)
        return
      }
      for (const h of this.handlers.get(msg.topic) ?? []) h(msg)
    }

    ws.onclose = () => {
      this.ws = null
      this.setState('closed')
      // Back off 0.5s, 1s, 2s … up to 15s, with jitter so clients don't reconnect in sync
      // after a deploy.
      const base = Math.min(15_000, 500 * 2 ** this.retries)
      this.retries++
      this.retryTimer = setTimeout(
        () => {
          this.retryTimer = undefined
          this.connect()
        },
        base / 2 + Math.random() * (base / 2),
      )
    }
  }
}

function socketUrl(): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/ws`
}

export const socket = new MarketSocket(socketUrl())
