# Security Policy

## Supported Versions

Security fixes are released for the latest published version of SMBark only.
Please upgrade before reporting an issue.

<!-- SUPPORTED_VERSION: updated automatically by `just release` -->
The current supported release is **v1.2.1**.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Report vulnerabilities privately using GitHub's
[private vulnerability reporting](https://github.com/z19r/smbark/security/advisories/new),
or by email to **zack@z19r.com**.

Please include:

- A description of the vulnerability and its impact.
- Steps to reproduce, or a proof-of-concept.
- The affected version(s) and your environment (OS, kernel, `cifs-utils` /
  `samba` versions).

You can expect an initial acknowledgement within **72 hours**. We will keep you
informed as we investigate, and will credit you in the release notes unless you
prefer to remain anonymous.

## Scope & Security Considerations

SMBark orchestrates privileged system operations. When assessing security,
please keep the following in mind:

- **Privileged commands** — SMBark shells out to `mount -t cifs`, `umount`,
  `systemctl`, and writes to `/etc/fstab` and systemd unit paths. These
  operations typically require `sudo`/root. Command-injection or
  path-traversal issues that let a share name, host, or mount option escape
  into a privileged command are in scope.
- **Credential handling** — SMB usernames and passwords are passed to
  `smbclient` and `mount.cifs`. Reports about credentials being leaked to logs,
  process listings, world-readable files, or shell history are in scope.
- **Generated configuration** — systemd `.mount`/`.automount` units and
  `/etc/fstab` entries are generated from user and network input. Malformed or
  hostile input that produces unsafe persistent configuration is in scope.
- **Network input** — data returned from `avahi-browse`, `nmblookup`, and
  `smbclient` originates from untrusted hosts on the network. Improper handling
  of that input is in scope.

Out of scope: vulnerabilities in the underlying tools (`cifs-utils`, `samba`,
`avahi`, `systemd`) themselves, and issues that require an already-root local
attacker.

## Disclosure

We follow coordinated disclosure. Once a fix is available, we will publish a
GitHub Security Advisory and note the fix in the [CHANGELOG](CHANGELOG.md).
