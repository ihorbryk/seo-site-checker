# Password hashing and CSRF defence for the `web` app

Research for issue #47 (parent map #44). Context: Go 1.27.2 (`go.mod`), `internal/web` on `net/http` ServeMux, `html/template`, htmx and plain form POSTs, email + password login with cookie sessions, a single `web` instance (ADR-0001).

## Recommendations

1. **Password hashing: argon2id** via `golang.org/x/crypto/argon2` (`argon2.IDKey`). Start at m=19456 KiB (19 MiB), t=2, p=1, which is OWASP's stated minimum, and tune upward against the host. Store a self-encoded string (PHC format) carrying the parameters so they can be raised later and old hashes rehashed on login. Bound concurrent hashes.
2. **CSRF: `http.CrossOriginProtection` is sufficient** for this app, with no tokens, provided that (a) all state changes are non-GET, (b) the session cookie is `SameSite=Lax` (or Strict), `Secure`, `HttpOnly`, preferably `__Host-` prefixed, (c) the site is served over HTTPS with HSTS, and (d) no sibling subdomain of the same registrable domain hosts untrusted content. If (d) cannot be guaranteed, add tokens or move to a dedicated domain.

## 1. Password hashing

### What OWASP says

Source: [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).

- Argon2id is the first choice: "Use Argon2id with a minimum configuration of 19 MiB of memory, an iteration count of 2, and 1 degree of parallelism."
- Equivalent minimums (m in KiB, t iterations, p=1): m=47104/t=1, m=19456/t=2, m=12288/t=3, m=9216/t=4, m=7168/t=5. They offer "an equal level of defense" and trade CPU for RAM.
- scrypt is the fallback when Argon2id is unavailable: N=2^17, r=8, p=1 minimum.
- bcrypt is for legacy systems only (where Argon2 and scrypt are unavailable): work factor as high as the server allows, minimum 10. bcrypt takes only 72 bytes for most implementations, so enforce a 72-byte maximum password length. Pre-hashing is discouraged as "dangerous" (null bytes, password shucking).
- PBKDF2 only when FIPS-140 is required (600,000 iterations for HMAC-SHA-256).
- Libraries should accept full Unicode, including NULL bytes, and not reduce the entropy of the input. A pepper is optional defence in depth, kept outside the database.

### What the Go libraries say

- [`argon2`](https://pkg.go.dev/golang.org/x/crypto/argon2): `IDKey` implements Argon2id; `Key` implements Argon2i and the docs say "If in doubt, prefer IDKey". The memory parameter is in KiB. The package's example uses the first RFC 9106 Section 4 option (time=1, memory=2 GiB, threads=4); the lower-memory fallback is time=3, 64 MiB, threads=4. The package offers no helper to encode or verify hash strings, only `IDKey`, `Key` and `Version`.
- [`bcrypt`](https://pkg.go.dev/golang.org/x/crypto/bcrypt): `GenerateFromPassword` returns `ErrPasswordTooLong` over 72 bytes. `DefaultCost` is 10, `MinCost` 4, `MaxCost` 31. It does provide `CompareHashAndPassword` and a self-describing hash string.

### Trade-offs for one small `web` instance

| | argon2id | bcrypt |
|---|---|---|
| OWASP status | Preferred | Legacy only |
| Resistance to GPU/ASIC cracking | Memory-hard | CPU-hard, 4 KiB memory, weaker against GPUs |
| Input limit | None | 72 bytes (must reject or truncate) |
| Library ergonomics | `IDKey` only; encoding and verification are written by us (about 40 lines: salt, PHC string, parse, `subtle.ConstantTimeCompare`) | Complete: `GenerateFromPassword` and `CompareHashAndPassword` |
| Server cost per login | Memory spike per concurrent hash (19 MiB at the minimum, 64 MiB at the RFC fallback) | Negligible memory |

For a single small instance the main operational risk of argon2id is memory: N concurrent logins allocate N x m. This is an inference from the documented parameters, not a documented library statement. Mitigation: a small semaphore (for example 2-4 concurrent hashes) around hashing, plus an existing or new login rate limit; otherwise a burst of login attempts becomes a cheap memory/CPU denial of service. bcrypt has the same CPU cost issue but not the memory one.

### Recommendation

Use argon2id. The extra code to encode and verify hashes is small and bcrypt's 72-byte limit and legacy OWASP status are worse costs.

- Floor: m=19456, t=2, p=1 (OWASP minimum). Pick the final values by benchmarking on the deployment host (a judgement call, not from a source): raise memory and iterations while a single hash stays comfortably interactive and the concurrency cap keeps worst-case memory acceptable. The RFC 9106 lower-memory option (t=3, 64 MiB) is a reasonable ceiling if the host has room.
- Store `$argon2id$v=19$m=...,t=...,p=...$salt$hash`; a random 16-byte salt from `crypto/rand`; a 32-byte output; compare with `subtle.ConstantTimeCompare`.
- On successful login, if the stored parameters are below current settings, rehash and update.
- Do not pre-hash and do not truncate. Do not impose a low maximum length (cap at something sane such as 1 KiB to limit DoS).
- A pepper is optional; skip it initially unless a secret store is already in place.

## 2. CSRF

### What `net/http.CrossOriginProtection` does

Sources: [package docs](https://pkg.go.dev/net/http#CrossOriginProtection) (added in Go 1.25.0) and the implementation, `src/net/http/csrf.go` in the Go 1.27.2 toolchain. `Check` runs, in order:

1. GET, HEAD, OPTIONS: always allowed ("It's important that applications do not perform any state changing actions due to requests with safe methods").
2. `Sec-Fetch-Site` present: `same-origin` or `none` allowed; any other value (`same-site`, `cross-site`) rejected unless exempt.
3. Neither `Sec-Fetch-Site` nor `Origin`: allowed ("either the request is same-origin or not a browser request").
4. `Origin` present: allowed if its host equals `req.Host`; otherwise rejected unless exempt.
5. Exemptions: `AddTrustedOrigin` (exact `scheme://host[:port]` match) and `AddInsecureBypassPattern` (ServeMux patterns, direct matches only).

Rejections respond 403 by default (`SetDenyHandler` customises it). Use: `http.NewCrossOriginProtection().Handler(mux)`.

The design rationale is in Filippo Valsorda's [CSRF writeup](https://words.filippo.io/csrf/): cookie-authenticated apps need CSRF protection, and tokens' "primary issue" is that "they require developers to instrument all their forms and other POST requests"; browsers now report cross-origin status themselves via Fetch Metadata, so tokens and keys are unnecessary.

### Fit with this app

- **Plain form POSTs and htmx POSTs** are same-origin browser requests, so they carry `Sec-Fetch-Site: same-origin` (or, in a pre-2023 browser, `Origin` equal to `Host`) and pass with no template or JS changes. No hidden fields and no `hx-headers` token plumbing. (That htmx requests send these headers is an inference from them being ordinary browser `fetch`/XHR calls; verify with a test.)
- **Login and logout POSTs** are covered too, so login CSRF is blocked as well as session riding.
- **Non-browser clients** (curl, scripts, tests) send neither header and pass. That is correct for CSRF: the attack requires a victim browser attaching ambient cookies, and a browser always sends at least `Origin` on cross-origin POST in post-2020 browsers. Such clients still need valid credentials, so the check is not an authentication control.

### Known gaps

Sources: Go source comments, Valsorda, and [OWASP CSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html).

- **Old browsers.** Without `Sec-Fetch-Site` (the docs say available in all browsers "since 2023"), the check falls back to `Origin` vs `Host`. Browsers that omit `Origin` on POST (some pre-2020) and send neither header are treated as non-browser and allowed, so those users are unprotected. OWASP calls an Origin fallback "mandatory" for Fetch Metadata implementations and also says "Missing headers and Origin: null are not evidence of a same-origin request" and that without a trusted source a token should be required. Go deliberately fails open here. For a small new app, accepting that trade-off is reasonable.
- **HTTP to HTTPS.** The `Origin` fallback compares host only, not scheme. The source comment states this "fail[s] open" for old browsers and says to mitigate with HSTS. `Sec-Fetch-Site` is sent only to trustworthy targets (Valsorda). Serve over HTTPS with HSTS.
- **Same-site subdomains.** `Sec-Fetch-Site: same-site` is rejected, so a sibling subdomain cannot post to us in a modern browser. But `SameSite` cookies cannot help here: OWASP says SameSite "is scoped to the registrable domain, not the origin", and Valsorda warns an XSS on an "old marketing blog" is easier than on the admin app. The residual risk is cookie tossing (a sibling domain setting cookies for the parent domain) rather than direct CSRF; a `__Host-` prefix cookie (no `Domain`, `Secure`, `Path=/`) prevents sibling domains overwriting the session cookie. Only relevant if other subdomains exist.
- **Safe-method state changes.** GET/HEAD/OPTIONS are never checked, and `SameSite=Lax` still sends cookies on cross-site top-level GET navigations (OWASP: "If any state-changing operation in the application is reachable via a GET request, SameSite=Lax will not stop it"). Keep every mutating route POST (or other unsafe method). Check htmx `hx-get` usage and any logout-by-link.
- **Reverse proxy.** If a proxy rewrites `Host` on plain-HTTP deployments, the Origin fallback can wrongly reject; the fix is `AddTrustedOrigin` (Valsorda). Behind a TLS-terminating proxy that preserves `Host`, no change is needed.
- **Privacy extensions that strip `Origin`**, and `Origin: null`: a stripped `Origin` with no `Sec-Fetch-Site` looks non-browser; modern browsers still send `Sec-Fetch-Site`, which the extension would have to strip too (Valsorda treats this as a vulnerability in the extension).
- **XSS defeats every CSRF defence** (OWASP). `html/template` auto-escaping is the main protection and is unrelated to this choice.
- **Bypass patterns** (`AddInsecureBypassPattern`) switch protection off for a route; avoid them unless a legitimate cross-site POST (webhook, SSO callback) exists. Webhooks would also be non-browser, though they still need their own authentication.

### Recommendation

Wrap the mux with `CrossOriginProtection` and do not add CSRF tokens. Pair it with:

- Session cookie: `HttpOnly`, `Secure`, `SameSite=Lax`, name `__Host-` prefixed, `Path=/`, no `Domain`.
- HSTS on the HTTPS site.
- Mutating routes accept only POST/PUT/PATCH/DELETE.
- A test that POSTs with `Sec-Fetch-Site: cross-site` (expect 403), `same-origin` (pass), and with a mismatching `Origin` and no `Sec-Fetch-Site` (expect 403), plus an htmx-style request.

Revisit and add tokens (OWASP: synchronizer token for stateful apps, since sessions are server-side) if the app must support pre-2020 browsers, or if untrusted content ever lives on a sibling subdomain.

## Sources

- OWASP Password Storage Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html
- OWASP CSRF Prevention Cheat Sheet: https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html
- Go `net/http.CrossOriginProtection`: https://pkg.go.dev/net/http#CrossOriginProtection and `src/net/http/csrf.go` (Go 1.27.2)
- `golang.org/x/crypto/argon2`: https://pkg.go.dev/golang.org/x/crypto/argon2
- `golang.org/x/crypto/bcrypt`: https://pkg.go.dev/golang.org/x/crypto/bcrypt
- Filippo Valsorda, CSRF: https://words.filippo.io/csrf/
