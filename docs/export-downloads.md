# Export download lifetime

The next-phases PRD promises 24-hour data-export links, but the shared storage
signer previously used its 15-minute artifact default for exports too. The
built-in Local and S3 drivers now implement ExpiringDownloads, allowing the export
handler to request a 24-hour download URL while ordinary uploads and release
artifact downloads keep their original 15-minute lifetime.

Completed export status/history responses include download_url,
download_expires_at, download_ttl_seconds and download_expiry_known. The console
shows the actual advertised expiry and a retry message if signing fails. Returning
to the authorized export page mints a fresh link. External/custom storage drivers
that only implement the old Storage interface remain compatible; their links are
returned without invented expiry metadata (download_expiry_known=false).

Download URLs are bearer credentials: anyone holding one can use it until expiry.
They contain exported player data and should not be shared publicly. This change
does not make claims of legal compliance, and does not change export ownership or
tenant authorization. Existing URLs retain their originally signed expiry.

The export page also captures workspace/game context before listing or submitting
jobs, preventing late dialog submission from targeting a newly selected game.

Validation was limited to Go formatting/parsing, console JavaScript syntax and
whitespace checks. Neither driver signing nor downloads were executed for this
slice under the shared-disk restriction.
