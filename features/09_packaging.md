# Feature 09: Binary Distribution / Packaging (stub)

Deferred follow-up to #12. Fleet may mix distros and arches; the binary is a
static `CGO_ENABLED=0` Go build, so one per-arch tarball already runs
unchanged everywhere. Recommended order: a **tarball-based `lanctl-upgrade`**
(using the existing tag-triggered release artifacts) with the same safety
levers as config sync (checksum, delay, kill switch, last-good rollback) —
universal and distro-agnostic. Native packages (`.deb` for Debian-family,
PKGBUILD for Arch) are optional, per-distro, and independent of each other.
To be implemented separately.
