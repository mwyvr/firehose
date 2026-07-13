# Deploying firehose

`firehose` runs as a batch job — fetch, render, write, exit — typically from
a systemd timer or cron. Nothing stays resident and nothing listens on a port.
Deploy locally or remote using the web infrastructure you already have.

## Get firehose

See the Makefile for options; there is a "vps" target if building for a virtual server with a limited instruction set.
Alternatively, download the appropriate pre-built binary from the [releases page](https://github.com/mwyvr/firehose/releases).

## Initial Deployment

1. Create a dedicated system user for firehose (no shell, no login)

```sh
useradd -r -s /sbin/nologin -d /var/lib/firehose -m firehose
```

2. Install the binary

```sh
install -m 0755 firehose-linux-amd64 /usr/local/bin/firehose
```

3. Create config and output directories

```sh
mkdir -p /etc/firehose /var/www/firehose
# generate an example config
/usr/local/bin/firehose init > /etc/firehose/config.toml
# edit config as needed
# set output_dir=/var/www/firehose,
# cache_db=/var/lib/firehose/cache.db
# add your feeds...
vi /etc/firehose/config.toml

# set ownership
chown -R firehose:firehose /var/www/firehose /var/lib/firehose
# substitute username for your username - you'll be editing the config
# from time to time to add feeds
chown <username>:firehose /etc/firehose/config.toml
chmod 0640 /etc/firehose/config.toml
```

4. Scheduling firehose runs (Linux)

Use a systemd service + timer, or (not shown) cron/cronie/alternatives if
using a non-systemd system to launch `firehose` at a polite (1 hour or longer)
interval. Some systems serving popular RSS feeds will lock you out if you hit them too often.

Create `/etc/systemd/system/firehose.service`:

```ini
[Unit]
Description=firehose feed generator
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=firehose
Group=firehose
ExecStartPre=/usr/local/bin/firehose -config /etc/firehose/config.toml check
ExecStart=/usr/local/bin/firehose -config /etc/firehose/config.toml
# hardening — the process needs exactly two writable paths
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ReadWritePaths=/var/www/firehose /var/lib/firehose
```

Create `/etc/systemd/system/firehose.timer`:

```ini
[Unit]
Description=hourly firehose run

[Timer]
OnCalendar=hourly
RandomizedDelaySec=5m
Persistent=true

[Install]
WantedBy=timers.target
```
Note: `RandomizedDelaySec` avoids fetching on the exact hour like every
other cron-driven reader probably is doing, a small extra politeness.
`Persistent=true` catches up a missed run after downtime. The `ExecStartPre`
check means a broken config edit fails the unit before it can touch the network
or the cache.

5. Fire off the first run 

```sh
systemctl daemon-reload
systemctl start firehose.service        # first run now
systemctl enable --now firehose.timer
```

You can also force a run at any time:

```sh
sudo -u firehose firehose -config /etc/firehose/config.toml -force
```

Or, to run `firehose` without a sudo password prompt:

```
<username> ALL=(root) NOPASSWD: /usr/bin/systemctl restart firehose.service
```

6. Verify

```sh
# check for status issues
systemctl status firehose.service --no-pager
# review the log
journalctl -u firehose.service -n 50
# ensure your HTML feeds were created
ls -l /var/www/firehose                 # rivers, style.css, river.js, firehose.html
```

## Example Web Server Configuration

Tip: If hosting firehose off a new subdomain, complete your DNS configuration.

### mox - subdomain example

Mox will issue an acme request for a subdomain shortly after reloading its configuration. 

In `domains.conf` add to your `WebHandlers` section:

```
WebHandlers:
	-
		LogName: firehose
		Domain: firehose.example.com
		PathRegexp: ^/
		Compress: true
		WebStatic:
			Root: /var/www/firehose
			ListFiles: false
			ContinueNotFound: false
			ResponseHeaders:
				Cache-Control: public, max-age=300
```

Run `mox config test` to verify your work, and restart/reload mox to apply.

### Example: Caddy

```
firehose.example.org {
	root * /var/www/firehose
	file_server
	encode zstd gzip
	header Cache-Control "public, max-age=300"
}
```

Caddy also handles certificates and HTTPS automatically.


**Why not run everything as your own user** (systemd user unit + linger,
config in ~, zero sudo ever)? Because the fetcher parses hostile XML from the
open web; if that ever goes wrong it should own a nologin account with two
writable directories, not your ssh keys. The dedicated user is the one piece
of ceremony worth keeping.

## SELinux (RHEL-family)

If the web server is denied read access to the output directory:

```sh
restorecon -Rv /var/www/firehose
# or persistently label it web content:
semanage fcontext -a -t httpd_sys_content_t '/var/www/firehose(/.*)?'
restorecon -Rv /var/www/firehose
```

(Label to whatever context your server's policy reads; mox setups vary.)

## Upgrades

One command from the workstation — builds for x86-64-v2, uploads, installs,
prints the deployed version as proof, and kicks an immediate run:

```sh
make deploy              # HOST=bugs by default; make deploy HOST=other
```

Manual equivalent, if you prefer the steps visible:

```sh
make vps && scp bin/firehose-linux-amd64 bugs:/tmp/
# on bugs:
sudo install -m 0755 /tmp/firehose-linux-amd64 /usr/local/bin/firehose
firehose version
sudo systemctl start firehose.service
```

No restart needed — the next timer run uses the new binary. The cache is
disposable; if a schema change ever requires it, delete
`/var/lib/firehose/cache.db` and the next run rebuilds it (you lose nothing but
one fetch cycle of dedupe history).
