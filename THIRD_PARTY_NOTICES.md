# Third-party notices

## Tencent openclaw-weixin

- Source: https://github.com/Tencent/openclaw-weixin
- Reference commit: `24de5c9eb0dd5e595d7e2d090ed8a3f82870d42c` (package compatibility version 2.4.9).
- Copyright (C) 2026 Tencent. All rights reserved.
- License: MIT. The complete original notice is preserved in `licenses/tencent-MIT.txt`.

This Go implementation adapts the protocol types, request headers, QR login
states, message builders, CDN URL behavior, AES-128-ECB/PKCS#7 pipeline and
media key encodings from these upstream files:

| Upstream reference | Go implementation |
|---|---|
| src/api/api.ts, src/api/types.ts | weixin/client.go, weixin/types.go |
| src/auth/login-qr.ts | weixin/login.go |
| src/messaging/send.ts | weixin/client.go, weixin/media.go |
| src/cdn/upload.ts, src/cdn/cdn-upload.ts, src/cdn/cdn-url.ts | weixin/media.go |
| src/cdn/aes-ecb.ts, src/cdn/pic-decrypt.ts | weixin/crypto.go, weixin/media.go |
| docs/quote-cache_zh_CN.md, ref_msg and partial_text wire types | weixin/types.go, internal/quotes/, cmd/relay/inbound_quotes.go |

No upstream TypeScript or OpenClaw runtime is required by the Go executable.
OpenClaw session routing, tools, plugins, account management and gateway were
not ported. Tencent's reference describes current client behavior rather than
guaranteeing the complete server contract; actual account compatibility must
be tested with the user's authorized account.

## skip2/go-qrcode

- Source: https://github.com/skip2/go-qrcode
- Version: `v0.0.0-20200617195104-da1b6568686e`.
- License: MIT; full notice retained in `vendor/github.com/skip2/go-qrcode/LICENSE`.
- Used by `cmd/weixin` only, to produce QR PNG and terminal display. The library
  is pure Go and is included in the built executable; it adds no external runtime.

The `dist` release directory includes copies of these notices and licenses.
