import {
  LEGAL_CONTACT,
  LEGAL_EFFECTIVE_DATE,
  LEGAL_GOVERNING_LAW,
  LEGAL_MIN_AGE,
  LEGAL_OPERATOR,
  LEGAL_VENUE,
} from '../lib/legal'

const Mail = () => <a href={`mailto:${LEGAL_CONTACT}`}>{LEGAL_CONTACT}</a>

export function TermsPage() {
  return (
    <main className="page about legal">
      <h1>Terms of Service</h1>
      <p className="muted">Effective {LEGAL_EFFECTIVE_DATE}</p>

      <p>
        These terms govern your use of PaperPerps (the “Service”), a paper-trading simulator operated by{' '}
        {LEGAL_OPERATOR} (“we”, “us”). By using the Service you agree to these terms and to our{' '}
        <a href="/privacy">Privacy Policy</a>. If you don’t agree, don’t use the Service.
      </p>

      <h2>1. Who can use PaperPerps</h2>
      <p>
        You must be at least {LEGAL_MIN_AGE} years old. If you’re under the age of majority where you live, you may use
        the Service only with the permission of a parent or guardian who agrees to these terms for you.
      </p>

      <h2>2. It’s a simulation: no real money, no real trades</h2>
      <ul>
        <li>
          Every balance, position, profit and loss on PaperPerps is simulated. Paper money has{' '}
          <strong>no monetary value</strong>. It can’t be deposited, withdrawn, transferred, sold or exchanged for
          anything, and we don’t award prizes.
        </li>
        <li>No order you place is sent to any exchange, broker or blockchain.</li>
        <li>
          PaperPerps is <strong>not affiliated with, endorsed by or connected to Hyperliquid</strong> or any exchange.
          We use publicly available market data. Product and asset names belong to their owners.
        </li>
      </ul>

      <h2>3. Not financial advice</h2>
      <p>
        Nothing on PaperPerps is investment, financial, legal or tax advice, or a recommendation to buy or sell anything.
        Simulated results are not indicative of real results. Real trading, particularly leveraged trading of
        perpetual futures, involves substantial risk, including losing more than you expected, and real markets
        behave differently from this simulation (see <a href="/about">How it works</a>).
      </p>

      <h2>4. Your account and recovery key</h2>
      <p>
        Accounts are anonymous. Access depends entirely on the secret key stored in your browser and in your recovery
        link. Anyone with your recovery link can use your account, so keep it private. If you lose it, we can’t recover
        your account. You can delete your account at any time from the account menu.
      </p>

      <h2>5. Acceptable use</h2>
      <p>You agree not to:</p>
      <ul>
        <li>
          interfere with, overload or attack the Service, or try to get around its rate limits or other protections;
        </li>
        <li>
          access the Service through automated means in a way that burdens it more than ordinary use, or scrape it at
          scale;
        </li>
        <li>
          create many accounts, or use any other trick, to manipulate the leaderboard;
        </li>
        <li>
          choose a handle that is offensive, harassing, misleading, impersonates someone, or infringes anyone’s rights;
        </li>
        <li>exploit bugs to gain an advantage instead of reporting them to us;</li>
        <li>use the Service for anything unlawful.</li>
      </ul>
      <p>
        We may change handles, hide or remove accounts from the leaderboard, reverse simulated trades caused by errors,
        and reset, suspend or delete accounts that break these terms, without notice.
      </p>

      <h2>6. Market data and simulation rules</h2>
      <p>
        Prices and other market data come from third parties and may be delayed, inaccurate or unavailable. The rules
        of the simulation (fills, fees, funding, margin, liquidations, leaderboard eligibility) are described in{' '}
        <a href="/about">How it works</a> and may change. If a simulated trade, liquidation or balance resulted from
        bad data or a bug, we may correct or reverse it.
      </p>

      <h2>7. Changes and availability</h2>
      <p>
        We may change, suspend or discontinue any part of the Service at any time, including resetting all accounts or
        leaderboards. We don’t promise the Service will be available, error-free or secure.
      </p>

      <h2>8. Our content</h2>
      <p>
        The Service, its design and its code are owned by us or our licensors. We give you a personal, non-exclusive,
        revocable right to use the Service under these terms. Charts use TradingView’s Lightweight Charts library under
        its license.
      </p>

      <h2>9. Disclaimer of warranties</h2>
      <p className="caps">
        The Service is provided “as is” and “as available”, without warranties of any kind, express or implied,
        including warranties of merchantability, fitness for a particular purpose, accuracy and non-infringement.
      </p>

      <h2>10. Limitation of liability</h2>
      <p className="caps">
        To the maximum extent permitted by law, we will not be liable for any indirect, incidental, special,
        consequential or punitive damages, or any loss of profits, data or goodwill, arising from or related to the
        Service, including any decision you make in real markets based on it. Our total liability for any claim related
        to the Service is limited to fifty U.S. dollars (US$50).
      </p>
      <p>Some jurisdictions don’t allow some of these limits, so they may not all apply to you.</p>

      <h2>11. Indemnity</h2>
      <p>
        You agree to indemnify and hold us harmless from claims and expenses (including reasonable legal fees) arising
        from your misuse of the Service or your breach of these terms.
      </p>

      <h2>12. Governing law</h2>
      <p>
        These terms are governed by the laws of {LEGAL_GOVERNING_LAW}, without regard to its conflict-of-law rules. Any
        dispute will be brought exclusively in {LEGAL_VENUE}, and you and we consent to their jurisdiction.
      </p>

      <h2>13. Changes to these terms</h2>
      <p>
        We may update these terms. We’ll change the effective date above, and for significant changes we’ll show a
        notice in the Service. Continuing to use the Service after a change means you accept it.
      </p>

      <h2>14. Contact</h2>
      <p>
        Questions about these terms: <Mail />.
      </p>
    </main>
  )
}

export function PrivacyPage() {
  return (
    <main className="page about legal">
      <h1>Privacy Policy</h1>
      <p className="muted">Effective {LEGAL_EFFECTIVE_DATE}</p>

      <p>
        PaperPerps is operated by {LEGAL_OPERATOR} (“we”, “us”). This policy explains what we collect when you use
        PaperPerps, why, and what you can do about it.
      </p>

      <section className="callout">
        <strong>In short:</strong> no sign-up, no names, emails, phone numbers or wallet addresses. No cookies, no
        analytics, no ads, no tracking. We never sell data. Your account is an anonymous key in your browser, and you can
        delete it, with its trading history, any time.
      </section>

      <h2>What’s stored in your browser</h2>
      <p>PaperPerps uses your browser’s local storage (not cookies) for:</p>
      <ul>
        <li>
          <code>paperperps.secret</code>: your account’s secret key. It’s what signs you in, so treat it like a
          password.
        </li>
        <li>
          <code>paperperps.recoveryNoticeDismissed</code>: whether you closed the “save your recovery link” notice.
        </li>
        <li>
          <code>paperperps.chartInterval</code>: the chart timeframe you picked.
        </li>
        <li>
          <code>paperperps.sounds</code> and <code>paperperps.desktopAlerts</code>: whether you want a sound and desktop
          notifications for fills and liquidations. Notifications are shown by your browser; nothing is sent to us or anyone
          else.
        </li>
        <li>
          <code>paperperps.priceAlerts</code>: the price alerts you set (coin and price). They’re checked in your browser
          and never sent to us.
        </li>
      </ul>
      <p>Clearing your browser’s site data removes them, and with the key gone you lose access to the account.</p>

      <h2>What we store on our servers</h2>
      <ul>
        <li>
          <strong>Your account:</strong> a random account ID, a one-way hash of your secret key (never the key itself),
          your handle (randomly generated unless you change it), whether you appear on the leaderboard, and when the
          account was created and last reset.
        </li>
        <li>
          <strong>Your simulated trading:</strong> orders, fills, positions, balances, fees, funding payments,
          liquidations, resets, and snapshots of your account value over time.
        </li>
        <li>
          <strong>Your trade journal:</strong> any notes you write on your trades. They’re private: only you can see
          them, and they’re never shown on your profile or the leaderboard. Please don’t put personal information in
          them.
        </li>
        <li>
          <strong>Moderation records:</strong> if we act on an account under our <a href="/terms">Terms</a> (for
          example renaming a handle, hiding it from the leaderboard, suspending, resetting or deleting it), we record
          the account ID, its handle at the time, what we did and why.
        </li>
      </ul>
      <p>
        We don’t ask for, and don’t want, anything that identifies you. Please don’t put personal information in your
        handle.
      </p>

      <h2>What’s public</h2>
      <p>
        If you’re eligible and haven’t hidden yourself, the leaderboard shows your handle, profit and loss, trading volume,
        number of trades and number of resets.
      </p>
      <p>
        While you’re shown on the leaderboard, you also have a public profile page (/u/your-handle) showing your profit and
        loss over time, win rate, best trade, trading volume, the date you started, statistics about your closed
        positions (such as average win and loss, holding time, and results by coin and by long or short), and your open
        positions (coin, size, entry price and leverage). It never shows your balance, orders, journal notes or recovery
        key.
      </p>
      <p>
        Turning off “Show me on the leaderboard” on the leaderboard page hides both, at any time. Share images you create
        are made in your browser and only go where you send them.
      </p>

      <h2>IP addresses and technical data</h2>
      <p>
        Like any website, our server sees your IP address with each request. We use it only in memory, briefly, to limit
        how many accounts can be created from one address and to protect the Service from abuse. We don’t store it in
        our database, and our server logs record only the requested page, the result and how long it took. Our hosting
        and infrastructure providers (currently Railway for hosting and Porkbun for the domain) may process IP addresses
        and similar request data to deliver and secure the Service, under their own privacy policies.
      </p>
      <p>
        Market data is fetched by our server from Hyperliquid’s public API. Your browser doesn’t contact Hyperliquid
        through PaperPerps, and we don’t share your information with them.
      </p>

      <h2>How we use information</h2>
      <p>
        Only to run the Service: to keep your account and simulated trades, show your history and the leaderboard,
        prevent abuse, and find and fix problems.
      </p>

      <h2>Sharing</h2>
      <p>
        We don’t sell, rent or share your information for advertising. We share it only with the service providers above
        as needed to run PaperPerps, or when required by law or to protect the rights and safety of users or the Service.
      </p>

      <h2>How long we keep it</h2>
      <ul>
        <li>Your account and trading history: until you delete the account, or we delete it or shut the Service down.</li>
        <li>Account value snapshots: 35 days, except the most recent older one, which ranks you on the leaderboard.</li>
        <li>
          Moderation records: kept after the account is deleted, so we can enforce our Terms. They contain no balances
          or trades.
        </li>
        <li>
          Backups: deleted data can remain in our database backups until they’re replaced, normally within 30 days.
        </li>
      </ul>

      <h2>Your choices and rights</h2>
      <ul>
        <li>
          <strong>Delete:</strong> account menu → <em>Delete account</em> permanently deletes your account and everything
          we store about it, immediately, except any moderation records described above.
        </li>
        <li>
          <strong>Access:</strong> your positions, orders, trade, order and funding history and journal notes are all
          visible in the app.
        </li>
        <li>
          <strong>Correct:</strong> change your handle on the leaderboard page.
        </li>
        <li>
          <strong>Hide:</strong> turn off “Show me on the leaderboard”.
        </li>
      </ul>
      <p>
        Depending on where you live, you may have further rights over personal information. Because accounts are
        anonymous, we can only act on an account you can prove is yours, which is why deletion happens from inside the
        account. For anything else, contact <Mail />.
      </p>

      <h2>Children</h2>
      <p>
        PaperPerps isn’t directed to children under {LEGAL_MIN_AGE}, and we don’t knowingly collect personal information
        from them. If you believe a child has given us personal information, contact us and we’ll delete it.
      </p>

      <h2>Security</h2>
      <p>
        Connections are encrypted (HTTPS), secret keys are stored only as one-way hashes, and recovery links keep the key
        in a part of the address that browsers don’t send to servers. No system is perfectly secure, so keep your recovery
        link private.
      </p>

      <h2>Where data is processed</h2>
      <p>PaperPerps is run from the United States, and our providers may process data there.</p>

      <h2>Changes</h2>
      <p>
        We may update this policy. We’ll change the effective date above, and for significant changes we’ll show a
        notice in the Service.
      </p>

      <h2>Contact</h2>
      <p>
        Questions or requests: <Mail />.
      </p>
    </main>
  )
}
