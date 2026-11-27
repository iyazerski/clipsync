# Host your own relay

By default, clipsync uses the free public relay at `clip.iyazerski.dev` (no uptime guarantee). You can host your own relay on any server with Docker, ports 80 and 443 open, and a domain pointing to it.

## Quick start with Docker Compose

1. On your first computer, open clipsync, click **Use your own relay**, enter your domain (e.g. `clip.example.com`), and click **Set up my first device**.
2. Open **Settings** and copy your **Relay token**.
3. Start the relay on your server:

```sh
curl -fsSLO https://raw.githubusercontent.com/iyazerski/clipsync/main/relay/compose.yaml
printf 'RELAY_DOMAIN=clip.example.com\nCLIPSYNC_TOKEN=<your token>\n' > .env
docker compose up -d
```

This runs behind [Caddy](https://caddyserver.com) with automatic HTTPS for your devices only. Set `CLIPSYNC_PUBLIC=true` instead of `CLIPSYNC_TOKEN` to allow anyone to use your relay.

## Already paired?

- **Desktop:** Open **Settings → Relay → Change** and enter your new relay address.
- **Android:** Unpair and join again using **Enter a code** with your relay address.

## Already have a reverse proxy?

Run just the relay container and proxy HTTPS to port 8080:

```sh
docker run -d --restart unless-stopped -p 127.0.0.1:8080:8080 \
  -e CLIPSYNC_TOKEN=<your token> ghcr.io/iyazerski/clipsync/relay
```

Your proxy must serve HTTPS and stream responses without buffering:

- **Caddy** and **Traefik** work out of the box.
- **nginx** needs `proxy_buffering off`.
- **Cloudflare quick tunnels** (`trycloudflare.com`) don't work because they buffer streams.
