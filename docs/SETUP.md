# Setting up a development machine

The quickest way to run GotATicket is Docker: see [Quick start with Docker](../README.md#quick-start-with-docker). The rest of this guide is for running it natively, which uses less memory and suits day-to-day development.

## Docker on an older Mac

Docker Desktop supports only the current and two previous major macOS releases and needs at least 4 GB of RAM, so on macOS 12 or a 4 GB machine it may not install, or may leave little memory for anything else. Colima is a lighter, command-line alternative (`brew install colima docker docker-compose`, then `colima start --memory 3`); the same `docker compose` commands then work. On a 4 GB Mac the native setup below is the more comfortable choice.

## Native setup

GotATicket needs three things installed: **Go 1.24+**, **Node.js 20.19+ or 22.12+**, and **MySQL 8.0.16+** (8.4 recommended). Redis, an SMTP server and Docker are optional.

## macOS

### Newer Macs (macOS 13 or later)

```bash
brew install go node mysql@8.4
brew services start mysql@8.4
```

### Older Intel Macs (macOS 12 Monterey and earlier)

Homebrew no longer supports macOS 12 officially, so it compiles many packages from source — slow and memory-hungry on a 4 GB machine. Use the official installers instead; they install in minutes:

1. **Go** — download the macOS *x86-64* `.pkg` for Go 1.24 or newer from <https://go.dev/dl/> and run it.
2. **Node.js** — download the macOS installer for the current 22.x LTS from <https://nodejs.org/> and run it.
3. **MySQL** — the current MySQL 8.4 installers target macOS 13 and later. On Monterey, open the MySQL Community Server archive (<https://downloads.mysql.com/archives/community/>), choose an 8.0.x version whose macOS download is built for macOS 12 (x86-64 DMG), and install it. Any MySQL **8.0.16 or newer** works. If you already run MySQL 8 for another project, reuse it.

After installing MySQL from the DMG, add its tools to your shell:

```bash
echo 'export PATH="/usr/local/mysql/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc
mysql --version
```

Start or stop the server from **System Preferences → MySQL**.

#### Keeping memory use low

On a 4 GB machine, give MySQL a small buffer pool. Create `/etc/my.cnf` (or edit it) with:

```ini
[mysqld]
innodb_buffer_pool_size = 128M
performance_schema = OFF
```

then restart MySQL. Leave `REDIS_URL` empty and run the API with the embedded worker (the defaults in `backend/.env.example`), so only two processes run: MySQL and the API, plus the Vite dev server while you work on the frontend.

`PASSWORD_HASH_ITERATIONS=100000` in the example `.env` keeps sign-in fast on an older CPU. Production must use the default of 600,000 or more; the API refuses to start in production with less.

## Linux (Debian/Ubuntu)

```bash
sudo apt install mysql-server     # or MySQL's own APT repository for 8.4
# Go: https://go.dev/doc/install   ·   Node.js: https://nodejs.org/en/download (or nvm)
```

## Then, on any system

```bash
git clone <your repository URL> gotaticket && cd gotaticket
make setup        # Go modules, npm packages, backend/.env
make db           # databases and the gotaticket MySQL user
make seed         # demo data
make api          # terminal 1
make web          # terminal 2 — open http://localhost:5173
```

No `make`? Every target is a one-line command in the `Makefile`; for example `make api` is `cd backend && go run ./cmd/gotaticket serve`.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `Access denied for user 'gotaticket'` | Run `make db` again, or check `DATABASE_DSN` in `backend/.env`. |
| `Unknown collation: 'utf8mb4_0900_ai_ci'` | You are on MariaDB or MySQL 5.7. GotATicket needs MySQL 8.0.16 or newer. |
| `make seed` logs "database already contains events; skipping demo data" | The demo seeder only fills an empty database. Drop and recreate it: `mysql -u root -p -e "DROP DATABASE gotaticket"` then `make db seed`. |
| Sign-in says "Too many failed sign-in attempts" | Ten wrong passwords lock an account for 15 minutes. Restart the API to clear it in development (the in-memory cache resets). |
| The scanner shows "No camera is available" | Cameras only work on `https://` or `localhost`. Type the ticket code instead, or use HTTPS. |
| Emails do not arrive | Without `SMTP_HOST`, emails are saved in `backend/storage/mail/` as `.eml` files. |
| Port 8080 or 5173 is busy | Set `HTTP_ADDR=:8081` and `API_URL=http://localhost:8081` in `backend/.env`, and start the web app with `VITE_API_PROXY=http://localhost:8081 npm run dev`. |
