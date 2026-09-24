# Configuration internals

[Developer guide](README.md) > Configuration internals

All in `config.go`. The operator-facing reference is
[Configuration](../user/configuration.md).

## Types

```go
type Duration time.Duration

type SMTPConfig struct {
    Host     string `json:"host"`
    Port     int    `json:"port"`
    Username string `json:"username"`
    Password string `json:"password"`
    From     string `json:"from"`
}

type Config struct {
    Listen                  string     `json:"listen"`
    PublicURL               string     `json:"public_url"`
    TrustedProxies          []string   `json:"trusted_proxies"`
    TrustedEmails           []string   `json:"trusted_emails"`
    StateFile               string     `json:"state_file"`
    WhitelistTTL            Duration   `json:"whitelist_ttl"`
    TokenTTL                Duration   `json:"token_ttl"`
    RequestsPerEmailPerHour int        `json:"requests_per_email_per_hour"`
    SMTP                    SMTPConfig `json:"smtp"`

    proxies []netip.Prefix // filled by validate
}
```

`proxies` is unexported and has no tag, so it can never be set from JSON.
Handlers use `cfg.proxies`, never `TrustedProxies`.

## `Duration`

`Duration` exists only so JSON can hold `"72h"` instead of nanoseconds.
`(*Duration).UnmarshalJSON` unmarshals the bytes into a `string` (so a JSON
number is an error: `cannot unmarshal number into Go value of type string`),
then `time.ParseDuration`. There is no `MarshalJSON`; config is never written
out. Code converts with `time.Duration(cfg.WhitelistTTL)`.

## `loadConfig`

```text
TYC_IPSET_NAME guard  ->  defaults  ->  JSON file (if TYC_CONFIG != "")  ->  applyEnv  ->  validate
```

0. If `os.LookupEnv("TYC_IPSET_NAME")` reports the variable as set, even to
   an empty string, return at once with
   `TYC_IPSET_NAME: no longer used; v0.2 gates via Caddy forward_auth, see README`
   and a nil config. This catches v0.1 compose files that would otherwise
   start with a silently ignored setting and no idea why Jellyfin moved. The
   JSON key `ipset_name` has no field any more, so a file containing it is
   accepted and the key ignored like any unknown key.
   `TestLoadConfigIpsetNameRejected` checks the exact message.
1. Build `&Config{...}` with the defaults: `Listen "127.0.0.1:8080"`,
   `TrustedProxies ["127.0.0.1", "::1"]`, `StateFile "/data/allowlist.json"`,
   `WhitelistTTL 72h`, `TokenTTL 15m`, `RequestsPerEmailPerHour 3`,
   `SMTP.Port 587`.
2. If `os.Getenv("TYC_CONFIG")` is non-empty, `os.ReadFile` it (error returned
   as is) and `json.Unmarshal` into the same struct (error wrapped as
   `<path>: <err>`). Unmarshalling over the defaults means keys absent from the
   file keep their defaults, and a list present in the file replaces the
   default list. Unknown keys are ignored.
3. `applyEnv(cfg)`. If it returns an error, `loadConfig` returns immediately,
   without validating.
4. `return cfg, cfg.validate()`. Callers must check the error; `cfg` is
   non-nil but not trustworthy when it is set.

## `applyEnv`

One `os.LookupEnv` per key, through four local helpers:

| Helper | Behaviour when the variable is set |
|--------|-------------------------------------|
| `str(key, *string)` | Assign the raw value. |
| `list(key, *[]string)` | `strings.Split(v, ",")`. No trimming here; `validate` trims. |
| `num(key, *int)` | `strconv.Atoi`; on error, append `key: err` and assign 0. |
| `dur(key, *Duration)` | `time.ParseDuration`; on error, append `key: err` and assign 0. |

`LookupEnv` distinguishes unset from empty: a variable set to `""` overrides
the file and default with an empty value. That is deliberate (it lets an
operator clear `TYC_TRUSTED_PROXIES`), and empty required fields are then
caught by `validate`.

All errors are collected and returned with `errors.Join`, in the order the keys
are applied.

## `validate`

Runs once, mutates the receiver, and returns every problem at once.

1. **Normalise emails**: lower-case, trim, drop empty entries; replace
   `TrustedEmails`. Each remaining entry must survive `mail.ParseAddress`
   unchanged (`a.Address == e`), so display-name forms and malformed
   addresses are reported as
   `TYC_TRUSTED_EMAILS (trusted_emails): invalid address "<value>"`.
2. **Parse proxies**: reset `proxies`, trim each entry, skip empty ones. If
   `netip.ParseAddr` succeeds, `Unmap()` and append
   `netip.PrefixFrom(ip, ip.BitLen())`, a `/32` or `/128`. Otherwise
   `netip.ParsePrefix` and append `pfx.Masked()`, so host bits are cleared
   (`172.28.0.5/24` becomes `172.28.0.0/24`). Entries that are neither, or a
   prefix length out of range such as `/33`, are reported as
   `TYC_TRUSTED_PROXIES (trusted_proxies): invalid address or CIDR "<value>"`.
   `TestLoadConfigTrustedProxiesCIDRAndBare` covers a mixed list and `/33`.
3. **Required strings**: `TYC_PUBLIC_URL`, `TYC_SMTP_HOST`,
   `TYC_SMTP_USERNAME`, `TYC_SMTP_PASSWORD`, `TYC_SMTP_FROM`, `TYC_LISTEN`,
   `TYC_STATE_FILE`. These are iterated from a map, so their order in the
   error message is not stable.
4. **Required list**: at least one trusted email after normalisation.
5. **Formats and ranges**: `PublicURL` absolute with a host and scheme
   `https`, or `http` only when the host is `localhost` or `127.0.0.1` (for
   local testing); `SMTP.From` accepted by `net/mail.ParseAddress`; `SMTP.Port` in
   1-65535; `RequestsPerEmailPerHour >= 1`; both TTLs `>= 1s`.

Each message has the form `TYC_NAME (json_key): problem`, so tests can assert
on the env var name and operators can find the key in either source.

`validate` does not open `StateFile`. `main` passes it to `newAllowlist` right
after `loadConfig` succeeds, and exits with `allowlist: <err>` if the file
exists but cannot be read or parsed (`TestLoadConfigStateFile` covers the
override and the empty-value error).

## Removing a setting

`TYC_IPSET_NAME` shows the pattern: delete the field, its default, its
`applyEnv` line and its `required` entry, and if operators are likely to still
set it, add a guard at the top of `loadConfig` that fails with a message
saying what replaced it. Keep the old key in `envKeys` in `config_test.go`,
as `TYC_IPSET_NAME` is, so `clearEnv` unsets it; otherwise a developer shell
that still exports it fails every config test.

## Adding a new setting

1. **Field**: add it to `Config` (or `SMTPConfig`) with a `json:"snake_case"`
   tag. Use `Duration` for durations and `[]string` for lists.
2. **Default**: set it in the `&Config{...}` literal in `loadConfig`, if it has
   one.
3. **Environment**: add one line to `applyEnv` using the right helper, named
   `TYC_` plus the upper-cased JSON key (`TYC_SMTP_` for SMTP fields).
4. **Validation**: add checks to `validate` using `bad("TYC_NAME (json_key)", "...")`.
   If it is required, add it to the `required` map.
5. **Use it**: read it in `newServer` or the handler that needs it. If it is a
   secret, make sure it never reaches a log line.
6. **Tests** in `config_test.go`:
   - add the env var to `envKeys`, so `clearEnv` isolates every test from the
     developer's environment;
   - if required, add it to `setRequiredEnv` and to the key list in
     `TestLoadConfigMissingRequiredNamesField`;
   - add the default to the assertions in `TestLoadConfigEnvOnly`;
   - add it to `fileConfig` and assert it in `TestLoadConfigFileOnly` and, if
     useful, `TestLoadConfigEnvOverridesFile`;
   - add a malformed value to `TestLoadConfigMalformedValues` (or
     `TestLoadConfigMalformedDuration`).
7. **Handler tests**: `newTestServer` in `handlers_test.go` builds `Config`
   directly, bypassing `loadConfig`. Set the new field there if handlers read
   it.
8. **Docs**: add a row to the tables in
   [the operator configuration page](../user/configuration.md) and in
   `README.md`, and to the compose example if operators usually set it.
