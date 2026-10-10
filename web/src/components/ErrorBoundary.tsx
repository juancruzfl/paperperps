import { Component, type ReactNode } from 'react'

type State = { error: Error | null }

/** Shows a recovery screen instead of a blank page if rendering crashes. */
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error) {
    console.error('render error', error)
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <div className="crash">
        <h1>Something broke on our side</h1>
        <p className="muted">Your account and positions are safe on the server. Reloading usually fixes this.</p>
        <button type="button" className="primary" onClick={() => location.reload()}>
          Reload
        </button>
        <pre className="muted small">{this.state.error.message}</pre>
      </div>
    )
  }
}
