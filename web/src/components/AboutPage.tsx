/** "How it works": what PaperPerps is, what it isn't, and exactly how the simulation behaves. */
export function AboutPage() {
  return (
    <main className="page about">
      <h1>How PaperPerps works</h1>

      <section className="callout">
        <strong>Paper trading only.</strong> Every dollar here is pretend. You can’t deposit, withdraw or win anything,
        and nothing you do is placed on any exchange or blockchain. PaperPerps is not affiliated with, endorsed by or
        connected to Hyperliquid. Nothing here is financial advice.
      </section>

      <h2>Your account</h2>
      <p>
        There’s no sign-up. Your browser gets an anonymous account with <strong>$1,000</strong> of mock USD the first
        time you visit. It’s tied to a secret key stored in your browser. Save your <em>recovery link</em> from the
        account menu to use the same account on another device. Anyone with that link can use your account, and if you
        lose it (for example by clearing your browser data) we can’t get the account back for you. We don’t collect
        names, emails or wallet addresses.
      </p>

      <h2>Prices</h2>
      <p>
        Charts, order books, trades, mark prices and funding rates come live from Hyperliquid’s public market data.
        If that feed stops, the affected markets show <em>Stale data</em>. Orders on them are refused and nothing is
        filled or liquidated until fresh prices arrive.
      </p>

      <h2>How your orders fill</h2>
      <ul>
        <li>
          <strong>Market orders</strong> fill against the real order book, level by level, so bigger orders get worse
          average prices, just as they would for real. They never fill more than 5% away from the mid price; any
          remainder is cancelled.
        </li>
        <li>
          <strong>Limit orders</strong> that cross the book fill immediately as a taker. The rest waits on the book and
          fills at your price once the market trades <em>through</em> it: a trade or a quote strictly better than your
          price. A trade exactly at your price doesn’t count, because we can’t know where you’d be in the queue.
        </li>
        <li>
          <strong>Post-only</strong> orders are refused if they would fill immediately. <strong>IOC</strong> orders
          fill what they can right away and cancel the rest.
        </li>
        <li>
          <strong>Stop and take-profit orders</strong> wait until the <em>mark price</em> reaches the trigger, then
          execute at market or, if you gave one, as a limit order. A take-profit/stop-loss pair attached to an order
          only becomes active once that order fills, and when one of the pair fills the other is cancelled.
        </li>
        <li>
          <strong>Reduce-only</strong> orders can only shrink a position, never grow or flip it.
        </li>
        <li>
          Your orders don’t move the real market. Everyone here trades against the same live book, so two people can
          “take” the same liquidity. That’s the price of simulating on real data.
        </li>
      </ul>

      <h2>Fees and funding</h2>
      <p>
        Fees match Hyperliquid’s base tier: <strong>0.045%</strong> when you take liquidity and{' '}
        <strong>0.015%</strong> when a resting order fills. Positions pay or receive <strong>funding</strong> at the
        top of every hour: size × oracle price × the live hourly rate. When the rate is positive, longs pay shorts.
      </p>

      <h2>Margin and liquidation</h2>
      <p>
        Positions use <strong>cross</strong> margin by default: they share your whole balance. In{' '}
        <strong>isolated</strong> mode a position only risks the margin you give it. A position is liquidated when its
        equity falls below the <em>maintenance margin</em>, half the margin needed at the market’s maximum leverage.
        It’s closed at market, and the margin left at stake is forfeited, like the worst case on Hyperliquid. You can
        never lose more than that: if prices gap past your liquidation price, the extra loss is absorbed, and your
        balance never goes below zero.
      </p>

      <h2>Leaderboard and resets</h2>
      <p>
        The leaderboard ranks <strong>profit and loss in USD</strong>, over 24 hours, 7 days, 30 days or all time. To
        appear you need at least 5 trades and an account at least 24 hours old, and you can hide yourself any time.
        You can reset to a fresh $1,000 once every 24 hours. A reset cancels your orders and wipes your positions, but
        your score keeps everything you made or lost, including the open positions’ profit or loss at the moment you
        reset.
      </p>
    </main>
  )
}
