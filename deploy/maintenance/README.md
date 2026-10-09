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
the catalog before making updated credentials eligible; empty catalogs and
transient failures retain known rules and leave new credentials pending. A
catalog HTTP 401 temporarily excludes that account from all configured models,
retaining its credentials and account identity for refresh or re-upload. This
known routing suspension does not block activating healthy accounts. A later
successful catalog check removes only those automatic exclusions, and bypasses
the catalog cache while authorization is pending. Recent successful checks are
cached for 30 minutes; each run is bounded to five minutes and saves verified
progress for a deferred retry. The daily account runner and its existing retry
queue use the same check.

`deploy/accounts/repair-gateway.ps1` applies the cloud gateway recovery policy
under the shared maintenance mutex and idle lease. It validates a staged
maintenance binary by SHA-256, saves private backups, verifies required models
against real account catalogs, and reloads the gateway with rollback on failure.
The policy keeps the account concurrency limit and allows 120 seconds for queued
slots and credential cooldowns, within a 240-second stream-open timeout. It
does not replace requested models with aliases or disable upstream cooldowns;
upstream terminal errors remain visible to clients. The default required model
is `gpt-5.6-luna`, which must be supported by a verified account before activation.
Required models are added to the global model list and the managed
`campus-worker` key's existing allowlist. Account scopes, other keys, and explicit
model exclusions are preserved.

Phone commands: `用量日报`, `账号状态`, `失效账号`, `更新状态`, `运维日报`.
`校园账号检查` and `校园账号状态` return the campus report directly.
`软件更新状态` reads the reports; `软件更新` or `检查软件更新` queues a native
check for both hosts. `校园软件更新` and `云端软件更新` select one host. Requests
are durable and deduplicated by inbound message and host; the existing retry
runners consume them within 15 minutes and publish their actual results. No
prompt, executable or AI job is stored in an update request. Failed execution
leaves the request pending; busy installation still uses the existing idle lease.

Linux Codex updates install the official musl package, including its matching
code-mode helper and packaged resources in their official layout. The updater verifies the release SHA-256,
reported version and required app-server schema before stopping the idle worker.
It switches the complete binary directory with a recoverable previous copy.
Windows updates stop matching task wrappers and retry short executable-file
release delays; an unsuccessful switch retains rollback and health checks.
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
Campus account follow-ups start at 07:00 Beijing time; the midnight retry does
not publish a second daily account report with yesterday's pool snapshot. Source
report IDs, timestamps and send receipts alone never count as changed results.
The cloud catalog retry resumes model-permission checks without repeating a
completed daily account check or its quota notification. A missing daily account
checkpoint is still retried, and failed report submissions retain their outbox.
An independent applied marker resumes a credential reload interrupted by a
catalog or health-check failure, without generating another account report.
Update notifications distinguish `软件更新待安装`, `软件更新完成` and
`软件更新检查`. A completion after a deferred morning check is a new result;
the original check time remains visible if WeChat accepts it later.
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
