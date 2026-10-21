# Deployment on the Ubuntu server

Copies of the files installed on `homesrv`; keep them in sync when changing the server.

| File | Installed at |
|------|--------------|
| `librarian.service` | `/etc/systemd/system/librarian.service` |
| `apache-lib.wildcar.ru.conf` | `/etc/apache2/sites-available/lib.wildcar.ru.conf` (`a2ensite lib.wildcar.ru`) |
| `certbot-reload-apache.sh` | `/etc/letsencrypt/renewal-hooks/deploy/reload-apache.sh` |

Secrets live in `/etc/librarian/librarian.env` (root:librarian 640), never here.

Update the running service:

```
go build -o bin/librarian ./cmd/librarian
sudo install -m 755 bin/librarian /usr/local/bin/librarian
sudo systemctl restart librarian
curl -s https://lib.wildcar.ru/health
```

Certificate: `certbot certonly --webroot -w /var/www/html -d lib.wildcar.ru` (the port-80 vhost keeps `/.well-known/acme-challenge/` open).

Connecting an assistant: add an MCP connector with URL `https://lib.wildcar.ru/mcp`; it opens the authorization
page, the owner allows it by the letter (ADR-0007) and can disconnect it on `/assistants`. Clients without OAuth
use the account token instead: header `Authorization: Bearer <token>` (`librarian token create --account <id>`;
one token per account, a new one replaces the old).

Web interface (ADR-0007): the same address, every path but `/mcp` and `/health`. The env file sets
`LIBRARIAN_PUBLIC_URL=https://lib.wildcar.ru`, `SMTP_ADDR=127.0.0.1:25` (local Postfix, no login) and
`MAIL_FROM=library@wildcar.ru`. An e-mail logs in only after `librarian account email <id> <e-mail>`;
`librarian login-link <e-mail>` prints a one-time link when a letter does not arrive.

Background processing: `serve` runs the pipeline for new files after uploads and hourly when `ROUTER_URL`/`ROUTER_TOKEN`
are set; `LIBRARIAN_AUTO_LIMIT_USD` (default 1) is the OCR estimate it may spend without the owner's «Обработать».
Uploaded files live in `/var/lib/librarian/accounts/<id>/files/`.
