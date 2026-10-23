import { useEffect, useState } from 'react'
import { api, ApiError } from '../lib/api'
import { refreshAccount, useAccount } from '../lib/account'
import { formatUsd, formatUsdCompact } from '../lib/format'
import { navigate, traderPath } from '../lib/route'
import { toast } from '../lib/toasts'
import type { Leaderboard, LeaderboardEntry, LeaderboardWindow } from '../lib/types'
import { CheckIcon, ChevronRightIcon, CircleIcon } from './Icons'

const WINDOWS: [LeaderboardWindow, string][] = [
  ['24h', '24 hours'],
  ['7d', '7 days'],
  ['30d', '30 days'],
  ['all', 'All time'],
]

export function LeaderboardPage() {
  const secret = useAccount((s) => s.secret)
  // Reload when the viewer's own profile changes, so a new handle or opt-out shows at once.
  const profileKey = useAccount((s) => (s.me ? `${s.me.handle}|${s.me.leaderboardOptIn}|${s.me.leaderboardHidden}|${!!s.me.suspension}|${s.me.resets}` : ''))
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
                  <th className="num" title="Profit and loss over this window, counting open positions at live prices">
                    PnL
                  </th>
                  <th className="num hide-sm" title="Total value of everything they traded">
                    Volume
                  </th>
                  <th className="num hide-sm">Trades</th>
                  <th className="num hide-sm" title="Times they started over with $1,000. Losses from before a reset still count">
                    Resets
                  </th>
                  <th aria-hidden />
                </tr>
              </thead>
              <tbody>
                {board.entries.map((e) => (
                  <Row key={e.handle} entry={e} />
                ))}
                {board.you && !youRanked && (
                  <>
                    <tr className="gap">
                      <td colSpan={7} />
                    </tr>
                    <Row entry={{ ...board.you, you: true }} unranked={!board.you.eligible} />
                  </>
                )}
              </tbody>
            </table>
          )}
          {board && (
            <p className="muted small board-foot">
              Select a trader to see their profile. Updated {new Date(board.updatedAt).toLocaleTimeString()}.
            </p>
          )}
        </section>

        <YourStanding board={board} windowLabel={WINDOWS.find(([w]) => w === window)?.[1] ?? ''} />
      </div>
    </main>
  )
}

function Row({ entry, unranked }: { entry: LeaderboardEntry; unranked?: boolean }) {
  const pnl = Number(entry.pnl)
  return (
    <tr className={entry.you ? 'you' : undefined} onClick={() => navigate(traderPath(entry.handle))}>
      <td className="num">
        {unranked ? '—' : entry.rank <= 3 ? <span className={`podium podium-${entry.rank}`}>{entry.rank}</span> : entry.rank}
      </td>
      <td>
        <a className="trader-link" href={traderPath(entry.handle)}>
          {entry.handle}
        </a>
        {entry.you && <span className="badge">you</span>}
        {entry.degen && <span className="badge degen" title="Has traded above a market’s real leverage cap (degen mode)">degen</span>}
      </td>
      <td className={`num ${pnl > 0 ? 'up' : pnl < 0 ? 'down' : ''}`}>{formatUsd(pnl, { signed: true })}</td>
      <td className="num hide-sm">{formatUsdCompact(Number(entry.volume))}</td>
      <td className="num hide-sm">{entry.trades}</td>
      <td className="num hide-sm">{entry.resets}</td>
      <td className="row-go" aria-hidden>
        <ChevronRightIcon size={14} />
      </td>
    </tr>
  )
}

/** The viewer's place on the board, what they still need to appear, and their settings. */
function YourStanding({ board, windowLabel }: { board: Leaderboard | null; windowLabel: string }) {
  const { me, secret } = useAccount()
  const [editing, setEditing] = useState(false)
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
        setEditing(false)
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not reach the server')
    } finally {
      setSaving(false)
    }
  }

  const you = board?.you
  const moderated = me.leaderboardHidden || !!me.suspension
  const lifetime = Number(me.lifetimePnl)
  const windowPnl = you ? Number(you.pnl) : 0
  const trades = you?.trades ?? 0
  // Measured at the board's update time, as the server does.
  const ageHours = board ? (Date.parse(board.updatedAt) - Date.parse(me.createdAt)) / 3_600_000 : 0
  const steps = board
    ? [
        {
          done: trades >= board.minTrades,
          label: `Make ${board.minTrades} trades`,
          detail: `${Math.min(trades, board.minTrades)} of ${board.minTrades}`,
        },
        {
          done: ageHours >= board.minAgeHours,
          label: `Keep your account ${board.minAgeHours} hours`,
          detail: `${Math.max(1, Math.ceil(board.minAgeHours - ageHours))}h to go`,
        },
      ]
    : []

  return (
    <section className="card profile">
      <h2>Your standing</h2>
      {moderated ? (
        <p className="notice-inline">A moderator has hidden this account from the leaderboard.</p>
      ) : !me.leaderboardOptIn ? (
        <p className="muted">You’re hidden from the leaderboard. Turn on “Show me on the leaderboard” below to appear.</p>
      ) : you?.eligible ? (
        <div className="standing">
          <span className="standing-rank">#{you.rank}</span>
          <span className="muted">
            {windowLabel.toLowerCase()} ·{' '}
            <span className={windowPnl > 0 ? 'up' : windowPnl < 0 ? 'down' : ''}>{formatUsd(windowPnl, { signed: true })}</span>
          </span>
        </div>
      ) : (
        board && (
          <div className="steps">
            <p className="muted">To appear on the board:</p>
            <ul>
              {steps.map((st) => (
                <li key={st.label} className={st.done ? 'done' : ''}>
                  {st.done ? <CheckIcon size={14} /> : <CircleIcon size={14} />}
                  <span>{st.label}</span>
                  <span className="muted">{st.done ? 'Done' : st.detail}</span>
                </li>
              ))}
            </ul>
          </div>
        )
      )}

      <dl className="profile-stats">
        <div>
          <dt>Lifetime PnL</dt>
          <dd className={lifetime > 0 ? 'up' : lifetime < 0 ? 'down' : ''}>{formatUsd(lifetime, { signed: true })}</dd>
        </div>
        <div>
          <dt title="Times you started over with $1,000. Losses from before a reset still count">Resets</dt>
          <dd>{me.resets}</dd>
        </div>
      </dl>
      {me.leaderboardOptIn && !moderated && (
        <a className="primary-link" href={traderPath(me.handle)}>
          View your public profile
        </a>
      )}

      <h2 className="profile-settings">Settings</h2>
      {editing ? (
        <form
          className="profile-form"
          onSubmit={(e) => {
            e.preventDefault()
            if (handle.trim()) void save({ handle: handle.trim() })
          }}
        >
          <label className="field">
            <span>New handle</span>
            <input
              value={handle}
              onChange={(e) => setHandle(e.target.value)}
              placeholder={me.handle}
              maxLength={24}
              autoComplete="off"
              autoFocus
            />
          </label>
          <span className="profile-form-actions">
            <button type="button" className="ghost small-btn" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <button type="submit" className="primary small-btn" disabled={saving || !handle.trim()}>
              Save
            </button>
          </span>
          <p className="muted small">Public. 3–24 lowercase letters, digits, - or _.</p>
        </form>
      ) : (
        <div className="setting-row">
          <span>
            <span className="muted">Handle</span> <span className="mono">{me.handle}</span>
          </span>
          {!me.suspension && (
            <button type="button" className="small-btn" onClick={() => setEditing(true)}>
              Change
            </button>
          )}
        </div>
      )}
      <label className="check">
        <input
          type="checkbox"
          checked={me.leaderboardOptIn}
          disabled={saving}
          onChange={(e) => void save({ leaderboardOptIn: e.target.checked })}
        />
        Show me on the leaderboard
      </label>
      <p className="muted small">Turning this off also hides your public profile.</p>
      {error && <p className="form-error">{error}</p>}
    </section>
  )
}
