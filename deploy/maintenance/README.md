# Native server maintenance

The campus installer and runner use Python 3.10 or newer with no extra packages.

The scripts and Go command make no model calls. Windows tasks and campus user
systemd timers run independently of the user's computer, in Beijing time.

- 07:00: approved stable software updates, then account checks.
- 00:00: previous completed calendar day's usage.
- Every 15 minutes: retry locally queued report submission and deferred idle-time
  installation. The relay tries each newest report every 30 minutes after a
  failed WeChat send, and records acceptance only on API success.

Managed software: cloud Codex CLI, Cockpit gateway and Caddy; campus Codex CLI,
Cockpit Tools and Clash Verge Rev. Custom Go relay/worker builds and ML/Python
environments have no external auto-update source and are preserved. These jobs
do not upgrade either operating system or automatically reboot it.

Official GitHub stable release metadata supplies asset SHA-256 digests. Campus
downloads through the existing private tunnel. No new public port is opened.
An atomic queue drain and exclusive cross-host lease prevent task claims while
binaries or account credentials are changed. Busy tasks defer maintenance.
Windows retains the previous executable; Linux GUI packages retain a root-owned
rollback .deb. Missing dependencies, checksum errors and incompatible CLI schema
prevent switching. No Clash subscription, system proxy or TUN setting is changed.

The cloud Cockpit `usage` events are the accounting authority. Requests are
deduplicated by request ID, account, model and start time, and attributed to their
completion time. Reasoning is a subset of output. Cache reads and writes receive
their own price; unsupported models/tiers are explicitly unpriced. The campus
report points to the same totals rather than charging them twice.

USD is an API-equivalent estimate, **not actual Codex subscription spending**.
Verified 2026-10-07 from official model pages:
[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol),
[GPT-6 Sol](https://developers.openai.com/api/docs/models/gpt-6-sol),
[GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna),
[pricing](https://developers.openai.com/api/docs/pricing),
[subscription pricing](https://learn.chatgpt.com/docs/pricing).
The report carries the price verification date. Tools, taxes and region premiums
are excluded; future price changes require updating the verified rate table.

Account checks use the usage endpoint rather than inference. Refresh credentials
within 26 hours of expiry; persist a rotated refresh token in the authority store
before any further request. Sidecar files receive access tokens only. Explicit
revoked/invalid refresh tokens or deactivated accounts are disabled and backed up
under `live/quarantine/<date>/<account-id>/`; native records remain recoverable.
Quota exhaustion, timeouts, unspecified 401/403 and server errors are retained.

The cloud also synchronizes each account's model catalog using GET
`/backend-api/codex/models`; this makes no inference request. Routing follows
returned model permissions, with no Team/Free/Plus plan allowlist. Automatic
exclusions are tracked separately and removed when permissions return. Existing
operator exclusions and API key scopes remain intact. Import activation checks
the catalog before making updated credentials eligible; empty/failed catalogs
retain known rules and leave new credentials pending. Recent catalog checks are
cached for 30 minutes; each run is bounded to five minutes and saves verified
progress for a deferred retry. The daily account runner and its existing retry
queue use the same check.

Phone commands: `用量日报`, `账号状态`, `失效账号`, `更新状态`, `运维日报`.
`校园账号检查` and `校园账号状态` return the campus report directly.
They query deterministic reports and do not create AI jobs. WeChat's reply-context
restriction can reject proactive notification; reports remain stored and queryable.
Maintenance never requests or unpauses old task images.

Reports are actively sent by a separate relay timer every 15 seconds, with no
inbound-message prerequisite. A rejected send is retried independently after
30 minutes; success is recorded only after the WeChat API accepts it. API
acceptance is not a handset read receipt, and does not prove indefinite validity
of the provider's reply-context credential.

The account report includes total/available/limited/disabled/unverified counts,
quota-window used and remaining percentages, reset times, and quarantine results.
HTTP 200 alone does not establish available quota. Missing quota fields remain
unverified; quota exhaustion never triggers permanent-account cleanup.
Campus account reports include the masked shared-pool entries, quota windows,
reset and quarantine results, plus their source check time. The campus separately
checks its authenticated relay health and model catalog, without inference or
copying account credentials. Missing or previous-day snapshots are explicitly
marked unverified or stale. The 15-minute retry publishes a follow-up when cloud
checks finish later; unchanged results are not sent again. An update lookup
failure does not suppress the campus morning account check.
Private queries can select a structured source with
`/maintenance/reports?host=cloud&kind=accounts`. This uses the existing private
Bearer authentication. Campus configuration adds `gateway_url` (the selected
Codex provider base URL) and `gateway_key_file` (the existing worker API key file).
Maintenance text is split at 2000 Unicode characters with durable per-part
receipts, so a rejected later part resumes without replaying accepted parts.
Numeric WeChat API rejection codes remain in the receipt; API rejection never
counts as successful notification.
Usage accounting has a separate runner lock so a simultaneous maintenance retry
cannot silently skip the midnight report. Windows native JSON is decoded as UTF-8
explicitly, including when run by Windows PowerShell 5.1 under SYSTEM.
