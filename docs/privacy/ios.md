# Debrid for iPhone · privacy

Debrid connects to the self-hosted server you choose. It does not include app analytics, advertising, tracking or a third-party telemetry service.

Your server receives the requests needed for the actions you take: signing in through its configured Pocket ID/OIDC provider, loading transfers and provider accounts, changing settings, and adding magnets or torrent files. The server and the debrid providers configured there handle that content under their own policies. Review the address before connecting. Network access to a private server may require Tailscale.

The main app stores its server-scoped session in the device Keychain, without iCloud Keychain synchronization. Non-secret connection metadata is stored in app preferences. Pending magnets, copied torrent documents and recoverable action receipts use protected local storage. The app and its Share Extension share only the protected pending inbox; the extension cannot read the app's session credentials and does not submit transfers to the server. Adding a shared item requires your review in the main app.

Signing out removes that server's local session and attempts server-side revocation. Pending items are managed separately so unsent work is not silently lost. Removing a transfer or provider account has its own confirmation; signing out does not delete server transfers or NAS files.

Diagnostic sharing is optional. The generated report uses a fixed redacted field list and excludes server addresses, account identities, credentials, magnet content and raw logs. Review it before sharing. Apple's TestFlight service can collect beta feedback and crash information under Apple's own terms; that is separate from an in-app analytics service.

The bundled Apple privacy manifest declares preferences access for app-owned metadata and file timestamps for app/group storage and documents you select. It declares no tracking or developer-collected data. Reassess these declarations if future app versions add external services or telemetry.
