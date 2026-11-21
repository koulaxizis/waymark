// Waymark relay as a Cloudflare Worker: the last way between an Agent and
// its Controller when they share no network (Waymark 0.1.1). It joins two
// WebSockets in a room and passes their bytes on. The bytes are the
// pairing's encrypted channel, so the relay never sees what goes through.
//
// Protocol (same as internal/relay in the app and waymark-relay):
//   GET /r/<room>?s=c   the Controller waits in its room
//   GET /r/<room>?s=a   an Agent joins the oldest waiting Controller
//   Authorization: Bearer <ACCESS_KEY>   when the ACCESS_KEY secret is set
// Binary messages go to the peer; "ping" is answered with "pong". When one
// side closes, the other is closed. Nothing is stored or logged.

const ROOM = /^\/r\/([0-9a-f]{32})$/;

export default {
  async fetch(req, env) {
    const url = new URL(req.url);
    const m = ROOM.exec(url.pathname);
    const side = url.searchParams.get("s");
    if (!m || (side !== "a" && side !== "c")) return new Response("Waymark relay\n", { status: 404 });
    if (req.headers.get("Upgrade") !== "websocket") return new Response("WebSocket only\n", { status: 426 });
    if (env.ACCESS_KEY) {
      const got = (req.headers.get("Authorization") || "").replace(/^Bearer /, "");
      if (!(await same(got, env.ACCESS_KEY))) return new Response("access key needed\n", { status: 401 });
    }
    return env.ROOMS.get(env.ROOMS.idFromName(m[1])).fetch(req);
  },
};

// same compares two strings in constant time, through their hashes so the
// lengths do not matter.
async function same(a, b) {
  const enc = new TextEncoder();
  const [x, y] = await Promise.all([a, b].map((s) => crypto.subtle.digest("SHA-256", enc.encode(s))));
  return crypto.subtle.timingSafeEqual(x, y);
}

// Room is one Controller's room. Sockets hibernate while quiet, so a
// Controller waiting all day costs nothing.
export class Room {
  constructor(state) {
    this.state = state;
    state.setWebSocketAutoResponse(new WebSocketRequestResponsePair("ping", "pong"));
  }

  async fetch(req) {
    const side = new URL(req.url).searchParams.get("s");
    const [client, server] = Object.values(new WebSocketPair());
    if (side === "c") {
      this.state.acceptWebSocket(server, ["c"]);
      server.serializeAttachment({ id: crypto.randomUUID(), peer: null, t: Date.now() });
      return new Response(null, { status: 101, webSocket: client });
    }
    const waiting = this.state
      .getWebSockets("c")
      .filter((ws) => !ws.deserializeAttachment()?.peer)
      .sort((x, y) => x.deserializeAttachment().t - y.deserializeAttachment().t);
    if (!waiting.length) {
      server.accept();
      server.close(1013, "no controller");
      return new Response(null, { status: 101, webSocket: client });
    }
    const c = waiting[0];
    const ca = c.deserializeAttachment();
    const id = crypto.randomUUID();
    this.state.acceptWebSocket(server, ["a"]);
    server.serializeAttachment({ id, peer: ca.id, t: Date.now() });
    ca.peer = id;
    c.serializeAttachment(ca);
    return new Response(null, { status: 101, webSocket: client });
  }

  peerOf(ws) {
    const peer = ws.deserializeAttachment()?.peer;
    if (!peer) return null;
    return this.state.getWebSockets().find((w) => w.deserializeAttachment()?.id === peer) || null;
  }

  async webSocketMessage(ws, msg) {
    if (typeof msg === "string") return; // only "ping", answered above
    const p = this.peerOf(ws);
    if (!p) return; // a Controller still waiting: nothing to pass on yet
    try {
      p.send(msg);
    } catch {
      this.end(ws);
    }
  }

  async webSocketClose(ws) {
    this.end(ws);
  }

  async webSocketError(ws) {
    this.end(ws);
  }

  end(ws) {
    const p = this.peerOf(ws);
    for (const w of [ws, p]) {
      if (!w) continue;
      try {
        w.close(1000, "closed");
      } catch {}
    }
  }
}
