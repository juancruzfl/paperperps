import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { formatUsd, formatUsdCompact } from '../lib/format'
import { toast } from '../lib/toasts'
import type { Leaderboard, LeaderboardEntry, LeaderboardWindow } from '../lib/types'

const WINDOWS: [LeaderboardWindow, string][] = [
  ['24h', '24 hours'],
  ['7d', '7 days'],
  ['30d', '30 days'],
  ['all', 'All time'],
]

export function LeaderboardPage() {
  const secret = useAccount((s) => s.secret)
  // Reload when the viewer's own profile changes, so a new handle or opt-out shows at once.
  const profileKey = useAccount((s) => (s.me ? `${s.me.handle}|${s.me.leaderboardOptIn}|${s.me.resets}` : ''))
  const [window, setWindow] = useState<LeaderboardWindow>('all')
  const [board, setBoard] = useState<Leaderboard | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    const load = () =>
      api.leaderboard(window, secret).then(
        (b) => {
          if (!cancelled) {
            setBoard(b)
            setError(null)
          }
        },
        (err: unknown) => {
          if (!cancelled) setError(err instanceof ApiError ? err.message : 'Could not reach the server')
        },
      )
    void load()
    const id = setInterval(load, 30_000) // the server recomputes standings every 30s
    return () => {
      cancelled = true
      clearInterval(id)
    }
  }, [window, secret, profileKey])

  const youRanked = board?.entries.some((e) => e.you)

  return (
    <main className="page">
      <div className="page-head">
        <div>
          <h1>Leaderboard</h1>
          <p className="muted">
            Ranked by profit and loss in USD. Resets give you a fresh $1,000 but your losses stay on the board.
          </p>
        </div>
        <div className="segmented page-windows" role="tablist" aria-label="Time window">
          {WINDOWS.map(([w, label]) => (
            <button key={w} type="button" role="tab" aria-selected={window === w} className={window === w ? 'active' : ''} onClick={() => setWindow(w)}>
              {label}
            </button>
          ))}
        </div>
      </div>

      <div className="page-grid">
        <section className="card board">
          {error && <p className="form-error">Couldn’t load the leaderboard: {error}</p>}
          {!board && !error && <div className="panel-empty">Loading…</div>}
          {board && board.entries.length === 0 && (
            <div className="panel-empty">
              Nobody qualifies yet. Make {board.minTrades} trades and keep your account for {board.minAgeHours} hours to
              be the first.
            </div>
          )}
          {board && board.entries.length > 0 && (
            <table className="data-table board-table">
              <thead>
                <tr>
                  <th className="num">#</th>
                  <th>Trader</th>
                  <th className="num">PnL</th>
                  <th className="num hide-sm">Volume</th>
                  <th className="num hide-sm">Trades</th>
                  <th className="num hide-sm">Resets</th>
                </tr>
              </thead>
              <tbody>
                {board.entries.map((e) => (
                  <Row key={e.handle} entry={e} />
                ))}
                {board.you && !youRanked && (
                  <>
                    <tr className="gap">
                      <td colSpan={6} />
                    </tr>
                    <Row entry={{ ...board.you, you: true }} unranked={!board.you.eligible} />
                  </>
                )}
              </tbody>
            </table>
          )}
          {board && (
            <p className="muted small board-foot">
              To appear: at least {board.minTrades} trades and an account at least {board.minAgeHours} hours old.
              Updated {new Date(board.updatedAt).toLocaleTimeString()}.
            </p>
          )}
        </section>

        <ProfileCard reason={board?.you && !board.you.eligible ? board.you.reason : undefined} />
      </div>
    </main>
  )
}

function Row({ entry, unranked }: { entry: LeaderboardEntry; unranked?: boolean }) {
  const pnl = Number(entry.pnl)
  return (
    <tr className={entry.you ? 'you' : undefined}>
      <td className="num">{unranked ? '—' : entry.rank <= 3 ? ['🥇', '🥈', '🥉'][entry.rank - 1] : entry.rank}</td>
      <td>
        {entry.handle}
        {entry.you && <span className="badge">you</span>}
      </td>
      <td className={`num ${pnl > 0 ? 'up' : pnl < 0 ? 'down' : ''}`}>{formatUsd(pnl, { signed: true })}</td>
      <td className="num hide-sm">{formatUsdCompact(Number(entry.volume))}</td>
      <td className="num hide-sm">{entry.trades}</td>
      <td className="num hide-sm">{entry.resets}</td>
    </tr>
  )
}

function ProfileCard({ reason }: { reason?: string }) {
  const { me, secret } = useAccount()
  const [handle, setHandle] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  if (!me || !secret) return <section className="card profile">Loading your account…</section>

  const save = async (profile: { handle?: string; leaderboardOptIn?: boolean }) => {
    setSaving(true)
    setError(null)
    try {
      await api.updateProfile(secret, profile)
      await refreshAccount()
      if (profile.handle) {
        toast(`You’re now ${profile.handle.toLowerCase()}`, 'success')
        setHandle('')
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  const lifetime = Number(me.lifetimePnl)
  return (
    <section className="card profile">
      <h2>You</h2>
      <dl className="profile-stats">
        <div>
          <dt>Handle</dt>
          <dd>{me.handle}</dd>
        </div>
        <div>
          <dt>Lifetime PnL</dt>
          <dd className={lifetime > 0 ? 'up' : lifetime < 0 ? 'down' : ''}>{formatUsd(lifetime, { signed: true })}</dd>
        </div>
        <div>
          <dt>Resets</dt>
          <dd>{me.resets}</dd>
        </div>
      </dl>
      {reason && <p className="notice-inline">Not on the board yet: {reason}.</p>}
      <form
        className="profile-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (handle.trim()) void save({ handle: handle.trim() })
        }}
      >
        <label className="field">
          <span>New handle</span>
          <input value={handle} onChange={(e) => setHandle(e.target.value)} placeholder={me.handle} maxLength={24} autoComplete="off" />
        </label>
        <button type="submit" className="primary" disabled={saving || !handle.trim()}>
          Save
        </button>
      </form>
      <label className="check">
        <input
          type="checkbox"
          checked={me.leaderboardOptIn}
          disabled={saving}
          onChange={(e) => void save({ leaderboardOptIn: e.target.checked })}
        />
        Show me on the leaderboard
      </label>
      {error && <p className="form-error">{error}</p>}
      <p className="muted small">Handles are public. 3–24 lowercase letters, digits, - or _.</p>
    </section>
  )
}
