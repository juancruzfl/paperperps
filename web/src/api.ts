export type Status = {
  version: string
  engine: 'leader' | 'standby'
  network: 'testnet' | 'mainnet'
  markets: string[]
  startingBalance: string
}

export async function fetchStatus(signal?: AbortSignal): Promise<Status> {
  const res = await fetch('/api/status', { signal })
  if (!res.ok) throw new Error(`status request failed: ${res.status}`)
  return res.json() as Promise<Status>
}
