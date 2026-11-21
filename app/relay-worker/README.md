# Waymark relay

The relay is the last way between a Waymark Agent and its Controller when
they share no network: cable, Wi-Fi and mobile data all failed, for
example when one laptop is on the venue Wi-Fi and the other on a phone's
4G. Both open a WebSocket to the relay and the relay joins them.

- **It sees only encrypted data.** The pairing's AES-256-GCM channel runs
  on top, unchanged. The relay never learns a key, a command or a file.
- **It is used only when nothing else works.** As soon as a network path
  answers again, the link moves back to it.
- **It is off until you set it.** Waymark ships with no relay. You run
  your own, so the traffic and the free-plan limits are yours.

## Option 1: Cloudflare (free plan)

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/koulaxizis/waymark/tree/main/app/relay-worker)

1. Press the button and sign in to Cloudflare (a free account is enough).
2. When it asks for `ACCESS_KEY`, type a long random password. Keep it:
   the Controller needs it.
3. When the deploy ends, copy the Worker's address, such as
   `https://waymark-relay.<you>.workers.dev`.

Or from a terminal, in this folder:

```sh
npx wrangler deploy
npx wrangler secret put ACCESS_KEY
```

The Worker refuses every connection until `ACCESS_KEY` is set. You can
also set or change it later in the Cloudflare dashboard: Workers & Pages,
waymark-relay, Settings, Variables and Secrets, Add, type Secret.

**Cost.** A Worker with a Durable Object fits the free plan. A waiting
Controller's socket hibernates and costs nothing. Slide commands are tiny.
A 100 MB file over the relay is about 1,600 messages, far below the daily
free limit. No storage is used.

## Option 2: your own server

Download `waymark-relay` for your system from the
[Releases](https://github.com/koulaxizis/waymark/releases) and run:

```sh
WAYMARK_RELAY_KEY='<long random password>' ./waymark-relay -addr :8443 -cert fullchain.pem -key privkey.pem
```

Without `-cert` it serves plain HTTP, for use behind a reverse proxy that
adds TLS (Caddy, nginx). The relay must be reachable from the internet.

## Turn it on in Waymark

On the **Controller** only: Settings, Relay. Fill in the address and the
access key, then Save. Paired Agents learn the relay over the encrypted
link within a second; nothing is set on the Agent. The Agent and the
Controller then show "over relay (internet)" whenever the link uses it.

## Protocol

```
GET <url>/r/<room>?s=c   the Controller waits in its room
GET <url>/r/<room>?s=a   an Agent joins the oldest waiting Controller
Authorization: Bearer <access key>
```

The room is a random name per Controller. Binary messages go to the peer;
the text message `ping` is answered with `pong`. When one side closes, the
other is closed. A message is at most 1 MiB on Cloudflare.
