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

Connecting an assistant: MCP URL `https://lib.wildcar.ru/mcp`, header `Authorization: Bearer <account token>`
(`librarian token create --account <id>`; one token per account, a new one replaces the old).
