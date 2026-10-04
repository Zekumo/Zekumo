export interface MiniCloudOptions {
  appId: string;
  baseUrl: string;
  token?: string;
  fetch?: typeof fetch;
  webSocket?: WebSocketConstructor;
}

export type Json = string | number | boolean | null | Json[] | { [key: string]: Json };

export interface Player {
  id: string;
  game_id: string;
  provider: string;
  identifier: string;
  nickname: string;
  profile: Json;
  banned: boolean;
  account_id?: string;
  created_at: string;
  last_login_at: string;
}

export interface LoginResult {
  token: string;
  player: Player;
}

export interface DataEntry {
  key: string;
  value?: Json;
  updated_at: string;
}

export interface LeaderboardEntry {
  rank: number;
  player_id: string;
  nickname: string;
  score: number;
}

export interface Achievement {
  id: string;
  key: string;
  name: string;
  description: string;
  icon_url: string;
  rarity: "common" | "rare" | "epic" | "legendary";
  hidden: boolean;
  type: "instant" | "progress";
  target: number;
  sort_order: number;
  unlocked: boolean;
  progress: number;
  unlocked_at: string | null;
}

export interface Friend {
  player_id: string;
  nickname: string;
  online: boolean;
  friend_since: string;
}

export interface FriendRequest {
  id: string;
  game_id: string;
  from_id: string;
  to_id: string;
  status: "pending" | "accepted" | "declined";
  created_at: string;
  updated_at: string;
  from_nickname?: string;
  to_nickname?: string;
}

export interface CurrencyBalance {
  currency_id: string;
  name: string;
  display_name: string;
  icon_url: string;
  balance: number;
}

export interface LedgerEntry {
  id: number;
  currency_id: string;
  player_id: string;
  amount: number;
  balance_after: number;
  kind: string;
  idempotency_key: string;
  note: string;
  created_at: string;
}

export interface SpendResult {
  currency_id: string;
  balance: number;
  duplicate: boolean;
}

export interface MailReward {
  currency_id: string;
  amount: number;
}

export interface MailboxItem {
  id: string;
  title: string;
  body: string;
  rewards: MailReward[];
  expires_at: string;
  created_at: string;
  read_at?: string;
  claimed_at?: string;
}

export interface ClaimResult {
  claimed_at: string;
  rewards: Array<{ currency_id: string; amount: number; balance: number }>;
}

export interface Announcement {
  id: string;
  game_id: string;
  title: string;
  body: string;
  importance: "info" | "warning" | "critical";
  platform: string;
  channel: string;
  active: boolean;
  expires_at?: string;
  created_at: string;
  updated_at: string;
}

export interface FunctionResult {
  result: Json;
  logs: string[];
}

export interface UpdateCheck {
  update_available: boolean;
  mandatory?: boolean;
  release?: { version: string; changelog: string; published_at: string };
  artifact?: { url: string; filename: string; size: number; sha256: string };
}

export interface Release {
  id: number;
  game_id: string;
  channel: string;
  version: string;
  changelog: string;
  status: string;
  mandatory: boolean;
  min_supported_version?: string;
  rollout_percent: number;
  published_at?: string;
  created_at: string;
}

export interface ClientLog {
  level?: "debug" | "info" | "warn" | "error";
  event?: string;
  message: string;
  fields?: Record<string, Json>;
}

export interface DialogueScript {
  id: string;
  game_id: string;
  script_key: string;
  title: string;
  content?: Json;
  version: number;
  updated_at: string;
}

export interface ChatMessage {
  id: number;
  game_id: string;
  channel: string;
  sender_id: string;
  sender_name: string;
  content: string;
  created_at: string;
}

export interface KVEntry {
  namespace: string;
  key: string;
  value: Json;
}

export interface Room {
  id: string;
  name: string;
  owner_id: string;
  max_players: number;
  meta?: Json;
  members: Array<{ player_id: string; nickname: string; state?: Json }>;
}

export class MiniCloudError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "MiniCloudError";
    this.status = status;
    this.code = code;
  }
}

type Query = Record<string, string | number | boolean | undefined>;

class Http {
  constructor(private readonly sdk: MiniCloud) {}

  async request<T>(
    method: string,
    path: string,
    opts: { body?: unknown; query?: Query; auth?: boolean } = {},
  ): Promise<T> {
    const url = new URL(this.sdk.baseUrl + path);
    for (const [k, v] of Object.entries(opts.query ?? {})) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = {};
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    if (opts.auth !== false) {
      if (!this.sdk.token) throw new MiniCloudError(0, "no_token", "call auth.loginAsGuest/login first");
      headers["Authorization"] = `Bearer ${this.sdk.token}`;
    }
    const doFetch = this.sdk.fetchImpl;
    const res = await doFetch(url.toString(), {
      method,
      headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
    });
    const text = await res.text();
    let parsed: unknown = undefined;
    if (text) {
      try {
        parsed = JSON.parse(text);
      } catch {
        parsed = undefined;
      }
    }
    if (!res.ok) {
      const err = (parsed as { error?: { code?: string; message?: string } } | undefined)?.error;
      throw new MiniCloudError(res.status, err?.code ?? "http_error", err?.message ?? `HTTP ${res.status}`);
    }
    return parsed as T;
  }
}

class AuthAPI {
  constructor(private readonly sdk: MiniCloud, private readonly http: Http) {}

  private async login(body: Record<string, unknown>): Promise<LoginResult> {
    const res = await this.http.request<LoginResult>("POST", "/v1/auth/login", {
      auth: false,
      body: { app_id: this.sdk.appId, ...body },
    });
    this.sdk.token = res.token;
    return res;
  }

  loginAsGuest(opts: { deviceId: string; nickname?: string }): Promise<LoginResult> {
    return this.login({ provider: "guest", device_id: opts.deviceId, nickname: opts.nickname });
  }

  loginWithPassword(opts: { username: string; password: string }): Promise<LoginResult> {
    return this.login({ provider: "password", username: opts.username, password: opts.password });
  }

  /** Exchange a one-time SSO ticket obtained from the hosted authorize page. */
  loginWithTicket(ticket: string): Promise<LoginResult> {
    return this.login({ provider: "sso", ticket });
  }

  async register(opts: { username: string; password: string; nickname?: string }): Promise<LoginResult> {
    const res = await this.http.request<LoginResult>("POST", "/v1/auth/register", {
      auth: false,
      body: { app_id: this.sdk.appId, provider: "password", ...opts },
    });
    this.sdk.token = res.token;
    return res;
  }

  logout(): void {
    this.sdk.token = undefined;
  }
}

class PlayerAPI {
  constructor(private readonly http: Http) {}

  getProfile(): Promise<Player> {
    return this.http.request("GET", "/v1/player/profile");
  }

  updateProfile(opts: { nickname?: string; profile?: Json }): Promise<Player> {
    return this.http.request("PUT", "/v1/player/profile", { body: opts });
  }

  bind(opts: { ticket?: string; username?: string; password?: string }): Promise<{ bound: boolean; account_id: string }> {
    if (!opts.ticket && !(opts.username && opts.password)) {
      return Promise.reject(new MiniCloudError(0, "bad_options", "ticket or username/password is required"));
    }
    return this.http.request("POST", "/v1/player/bind", { body: opts });
  }
}

class PlayerDataAPI {
  constructor(private readonly http: Http) {}

  async list(): Promise<DataEntry[]> {
    const res = await this.http.request<{ entries: DataEntry[] }>("GET", "/v1/player/data");
    return res.entries;
  }

  async get<T extends Json = Json>(key: string): Promise<T> {
    const res = await this.http.request<{ value: T }>("GET", `/v1/player/data/${encodeURIComponent(key)}`);
    return res.value;
  }

  set(key: string, value: Json): Promise<DataEntry> {
    return this.http.request("PUT", `/v1/player/data/${encodeURIComponent(key)}`, { body: value });
  }

  delete(key: string): Promise<void> {
    return this.http.request("DELETE", `/v1/player/data/${encodeURIComponent(key)}`);
  }
}

class LeaderboardsAPI {
  constructor(private readonly http: Http) {}

  async submit(board: string, score: number, mode?: "max" | "incr" | "replace"): Promise<number> {
    const res = await this.http.request<{ score: number }>(
      "POST",
      `/v1/leaderboards/${encodeURIComponent(board)}/score`,
      { body: { score, mode } },
    );
    return res.score;
  }

  async top(board: string, opts: { offset?: number; limit?: number; scope?: "friends" } = {}): Promise<LeaderboardEntry[]> {
    const res = await this.http.request<{ entries: LeaderboardEntry[] }>(
      "GET",
      `/v1/leaderboards/${encodeURIComponent(board)}`,
      { query: { offset: opts.offset, limit: opts.limit, scope: opts.scope } },
    );
    return res.entries;
  }

  me(board: string): Promise<LeaderboardEntry> {
    return this.http.request("GET", `/v1/leaderboards/${encodeURIComponent(board)}/me`);
  }
}

class AchievementsAPI {
  constructor(private readonly http: Http) {}

  async list(): Promise<Achievement[]> {
    const res = await this.http.request<{ achievements: Achievement[] }>("GET", "/v1/achievements");
    return res.achievements;
  }

  async unlocked(): Promise<Achievement[]> {
    const res = await this.http.request<{ unlocked: Achievement[] }>("GET", "/v1/achievements/unlocked");
    return res.unlocked;
  }
}

class FriendsAPI {
  constructor(private readonly http: Http) {}

  sendRequest(targetPlayerId: string): Promise<FriendRequest> {
    return this.http.request("POST", "/v1/friends/request", { body: { target_player_id: targetPlayerId } });
  }

  accept(requestId: string): Promise<void> {
    return this.http.request("POST", "/v1/friends/accept", { body: { request_id: requestId } });
  }

  decline(requestId: string): Promise<void> {
    return this.http.request("POST", "/v1/friends/decline", { body: { request_id: requestId } });
  }

  async list(): Promise<Friend[]> {
    const res = await this.http.request<{ friends: Friend[] }>("GET", "/v1/friends");
    return res.friends;
  }

  async requests(): Promise<FriendRequest[]> {
    const res = await this.http.request<{ requests: FriendRequest[] }>("GET", "/v1/friends/requests");
    return res.requests;
  }

  remove(playerId: string): Promise<void> {
    return this.http.request("DELETE", `/v1/friends/${encodeURIComponent(playerId)}`);
  }

  block(targetPlayerId: string): Promise<void> {
    return this.http.request("POST", "/v1/friends/block", { body: { target_player_id: targetPlayerId } });
  }
}

class CurrencyAPI {
  constructor(private readonly http: Http) {}

  async balances(): Promise<CurrencyBalance[]> {
    const res = await this.http.request<{ balances: CurrencyBalance[] }>("GET", "/v1/currency");
    return res.balances;
  }

  async ledger(currencyId: string, opts: { limit?: number; offset?: number } = {}): Promise<LedgerEntry[]> {
    const res = await this.http.request<{ entries: LedgerEntry[] }>(
      "GET",
      `/v1/currency/${encodeURIComponent(currencyId)}/ledger`,
      { query: { limit: opts.limit, offset: opts.offset } },
    );
    return res.entries;
  }

  spend(currencyId: string, amount: number, idempotencyKey: string, note?: string): Promise<SpendResult> {
    return this.http.request("POST", `/v1/currency/${encodeURIComponent(currencyId)}/spend`, {
      body: { amount, idempotency_key: idempotencyKey, note },
    });
  }
}

class MailboxAPI {
  constructor(private readonly http: Http) {}

  list(opts: { unreadOnly?: boolean } = {}): Promise<{ mail: MailboxItem[]; unread: number }> {
    return this.http.request("GET", "/v1/mailbox", {
      query: { unread_only: opts.unreadOnly ? "true" : undefined },
    });
  }

  markRead(mailId: string): Promise<void> {
    return this.http.request("POST", `/v1/mailbox/${encodeURIComponent(mailId)}/read`, { body: {} });
  }

  claim(mailId: string): Promise<ClaimResult> {
    return this.http.request("POST", `/v1/mailbox/${encodeURIComponent(mailId)}/claim`, { body: {} });
  }
}

class AnnouncementsAPI {
  constructor(private readonly sdk: MiniCloud, private readonly http: Http) {}

  async list(opts: { afterId?: string; platform?: string; channel?: string } = {}): Promise<Announcement[]> {
    const res = await this.http.request<{ announcements: Announcement[] }>(
      "GET",
      `/v1/apps/${encodeURIComponent(this.sdk.appId)}/announcements`,
      { auth: false, query: { after_id: opts.afterId, platform: opts.platform, channel: opts.channel } },
    );
    return res.announcements;
  }
}

class FunctionsAPI {
  constructor(private readonly sdk: MiniCloud, private readonly http: Http) {}

  /** Call a cloud function as the logged-in player. */
  call(name: string, body?: Json): Promise<FunctionResult> {
    const path = `/v1/functions/${encodeURIComponent(name)}`;
    if (body === undefined) return this.http.request("GET", path);
    return this.http.request("POST", path, { body });
  }

  /** Call a public cloud function without logging in. */
  callPublic(name: string, body?: Json): Promise<FunctionResult> {
    const path = `/v1/apps/${encodeURIComponent(this.sdk.appId)}/functions/${encodeURIComponent(name)}`;
    if (body === undefined) return this.http.request("GET", path, { auth: false });
    return this.http.request("POST", path, { auth: false, body });
  }
}

class UpdatesAPI {
  constructor(private readonly sdk: MiniCloud, private readonly http: Http) {}

  check(opts: {
    version: string;
    platform: string;
    arch?: string;
    channel?: string;
    deviceId?: string;
  }): Promise<UpdateCheck> {
    return this.http.request("GET", `/v1/apps/${encodeURIComponent(this.sdk.appId)}/updates/check`, {
      auth: false,
      query: {
        version: opts.version,
        platform: opts.platform,
        arch: opts.arch,
        channel: opts.channel,
        device_id: opts.deviceId,
      },
    });
  }

  async releases(): Promise<Release[]> {
    const res = await this.http.request<{ releases: Release[] }>(
      "GET", `/v1/apps/${encodeURIComponent(this.sdk.appId)}/releases`, { auth: false },
    );
    return res.releases;
  }
}

class LogsAPI {
  constructor(private readonly http: Http) {}

  report(entry: ClientLog): Promise<{ accepted: boolean }> {
    return this.http.request("POST", "/v1/logs", { body: entry });
  }
}

class DialoguesAPI {
  constructor(private readonly http: Http) {}

  async list(): Promise<DialogueScript[]> {
    const res = await this.http.request<{ scripts: DialogueScript[] }>("GET", "/v1/dialogues");
    return res.scripts;
  }

  get(key: string): Promise<DialogueScript> {
    return this.http.request("GET", `/v1/dialogues/${encodeURIComponent(key)}`);
  }
}

class ChatAPI {
  constructor(private readonly http: Http) {}

  async history(channel: string, opts: { beforeId?: number; limit?: number } = {}): Promise<ChatMessage[]> {
    const res = await this.http.request<{ messages: ChatMessage[] }>("GET", "/v1/chat/history", {
      query: { channel, before_id: opts.beforeId, limit: opts.limit },
    });
    return res.messages;
  }
}

class KVAPI {
  constructor(private readonly http: Http) {}

  /** Read one key from a public namespace ("public" or "public_*"). */
  get<T extends Json = Json>(namespace: string, key: string): Promise<KVEntry & { value: T }> {
    return this.http.request("GET", `/v1/kv/${encodeURIComponent(namespace)}/${encodeURIComponent(key)}`);
  }

  async list(
    namespace: string,
    opts: { limit?: number; offset?: number } = {},
  ): Promise<Array<{ key: string; updated_at: string }>> {
    const res = await this.http.request<{ keys: Array<{ key: string; updated_at: string }> }>(
      "GET",
      `/v1/kv/${encodeURIComponent(namespace)}`,
      { query: { limit: opts.limit, offset: opts.offset } },
    );
    return res.keys;
  }
}

// ---------------------------------------------------------------------------
// Realtime (WebSocket)
// ---------------------------------------------------------------------------

interface WebSocketLike {
  readonly readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: string, listener: (event: never) => void): void;
}

export type WebSocketConstructor = new (url: string) => WebSocketLike;

export type RealtimeEvent =
  | "open"
  | "close"
  | "reconnecting"
  | "replaced"
  | "welcome"
  | "error"
  | "pong"
  | "room.created"
  | "room.joined"
  | "room.left"
  | "room.list"
  | "room.member_joined"
  | "room.member_left"
  | "room.state"
  | "room.msg"
  | "chat.subbed"
  | "chat.unsubbed"
  | "chat.msg"
  | (string & {});

type Listener = (data: never) => void;

export interface RealtimeEventData {
  open: { reconnected: boolean };
  close: { code?: number; reason?: string };
  reconnecting: { attempt: number; delay: number };
  replaced: Record<string, never>;
  welcome: { player_id: string; nickname: string };
  error: { code?: string; message?: string };
  "room.created": Room;
  "room.joined": Room;
  "room.left": Record<string, never>;
  "room.list": { rooms: Room[] };
  "room.member_joined": { player_id: string; nickname: string };
  "room.member_left": { player_id: string; new_owner: string };
  "room.state": { player_id: string; state: Json };
  "room.msg": { player_id: string; data: Json };
  "chat.subbed": { channel: string };
  "chat.unsubbed": { channel: string };
  "chat.msg": ChatMessage;
}

const OPEN = 1;
const REPLACED_CODE = 1008;
const REQUEST_TIMEOUT_MS = 10_000;
const PING_INTERVAL_MS = 25_000;
const MAX_BACKOFF_MS = 30_000;

export class RealtimeClient {
  private ws?: WebSocketLike;
  private listeners = new Map<string, Set<Listener>>();
  private chatSubs = new Set<string>();
  private pingTimer?: ReturnType<typeof setInterval>;
  private reconnectTimer?: ReturnType<typeof setTimeout>;
  private attempts = 0;
  private manuallyClosed = false;
  private everConnected = false;
  private connecting?: Promise<void>;

  constructor(private readonly sdk: MiniCloud) {}

  get connected(): boolean {
    return this.ws?.readyState === OPEN;
  }

  connect(): Promise<void> {
    if (this.connected) return Promise.resolve();
    if (this.connecting) return this.connecting;
    if (!this.sdk.token) return Promise.reject(new MiniCloudError(0, "no_token", "log in before connecting"));
    this.manuallyClosed = false;
    const pending = this.open();
    const wrapped = pending.finally(() => {
      if (this.connecting === wrapped) this.connecting = undefined;
    });
    this.connecting = wrapped;
    return wrapped;
  }

  private wsURL(): string {
    const base = this.sdk.baseUrl.replace(/^http/, "ws");
    return `${base}/v1/ws?token=${encodeURIComponent(this.sdk.token ?? "")}`;
  }

  private open(): Promise<void> {
    const WS = this.sdk.webSocketImpl;
    if (!WS) {
      return Promise.reject(
        new MiniCloudError(0, "no_websocket", "no WebSocket available; pass one via options.webSocket"),
      );
    }
    return new Promise((resolve, reject) => {
      const ws = new WS(this.wsURL());
      this.ws = ws;
      let settled = false;

      ws.addEventListener("open", () => {
        if (this.ws !== ws) {
          ws.close(1000, "stale connection");
          return;
        }
        settled = true;
        this.attempts = 0;
        const resub = this.everConnected;
        this.everConnected = true;
        this.startPing();
        this.emit("open", { reconnected: resub });
        if (resub) for (const ch of this.chatSubs) this.send("chat.sub", { channel: ch });
        resolve();
      });
      ws.addEventListener("message", (event: { data: unknown }) => {
        let env: { type?: string; data?: unknown };
        try {
          env = JSON.parse(String((event as { data: unknown }).data));
        } catch {
          return;
        }
        if (env.type) this.emit(env.type, env.data);
      });
      ws.addEventListener("close", (event: { code?: number; reason?: string }) => {
        if (this.ws !== ws) return;
        this.stopPing();
        const e = event as { code?: number; reason?: string };
        this.emit("close", { code: e.code, reason: e.reason });
        if (e.code === REPLACED_CODE) {
          this.emit("replaced", {});
          return;
        }
        if (!this.manuallyClosed) this.scheduleReconnect();
        if (!settled) {
          settled = true;
          reject(new MiniCloudError(0, "ws_closed", e.reason || "connection closed"));
        }
      });
      ws.addEventListener("error", () => {
        if (!settled && ws.readyState !== OPEN) {
          // The close event follows and carries the retry logic.
        }
      });
    });
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return;
    const backoff = Math.min(1000 * 2 ** this.attempts, MAX_BACKOFF_MS);
    const delay = backoff / 2 + Math.random() * (backoff / 2);
    this.attempts += 1;
    this.emit("reconnecting", { attempt: this.attempts, delay });
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = undefined;
      if (!this.manuallyClosed) this.open().catch(() => undefined);
    }, delay);
  }

  private startPing(): void {
    this.stopPing();
    this.pingTimer = setInterval(() => this.send("ping", {}), PING_INTERVAL_MS);
  }

  private stopPing(): void {
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.pingTimer = undefined;
  }

  close(): void {
    this.manuallyClosed = true;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.reconnectTimer = undefined;
    this.stopPing();
    this.ws?.close(1000, "client closed");
    this.ws = undefined;
  }

  on<K extends RealtimeEvent>(type: K, listener: (data: K extends keyof RealtimeEventData ? RealtimeEventData[K] : unknown) => void): () => void {
    let set = this.listeners.get(type);
    if (!set) {
      set = new Set();
      this.listeners.set(type, set);
    }
    set.add(listener as Listener);
    return () => this.off(type, listener);
  }

  off<K extends RealtimeEvent>(type: K, listener: (data: K extends keyof RealtimeEventData ? RealtimeEventData[K] : unknown) => void): void {
    this.listeners.get(type)?.delete(listener as Listener);
  }

  private emit(type: string, data: unknown): void {
    for (const fn of this.listeners.get(type) ?? []) {
      (fn as (d: unknown) => void)(data);
    }
  }

  send(type: string, data?: unknown): void {
    if (!this.connected || !this.ws) {
      throw new MiniCloudError(0, "not_connected", "realtime connection is not open");
    }
    this.ws.send(JSON.stringify({ type, data }));
  }

  /** Send a request and resolve on the expected reply (or reject on "error"). */
  private request<T>(sendType: string, data: unknown, replyType: string): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const cleanup = () => {
        this.off(replyType as RealtimeEvent, onReply as (data: unknown) => void);
        this.off("error", onError as (data: unknown) => void);
        clearTimeout(timer);
      };
      const onReply = (reply: T) => {
        cleanup();
        resolve(reply);
      };
      const onError = (err: { code?: string; message?: string }) => {
        cleanup();
        reject(new MiniCloudError(0, err.code ?? "ws_error", err.message ?? "realtime error"));
      };
      const timer = setTimeout(() => {
        cleanup();
        reject(new MiniCloudError(0, "timeout", `no ${replyType} reply within 10s`));
      }, REQUEST_TIMEOUT_MS);
      this.on(replyType, onReply as (d: unknown) => void);
      this.on("error", onError);
      try {
        this.send(sendType, data);
      } catch (err) {
        cleanup();
        reject(err);
      }
    });
  }

  createRoom(opts: { name?: string; maxPlayers?: number; meta?: Json } = {}): Promise<Room> {
    return this.request("room.create", { name: opts.name, max_players: opts.maxPlayers, meta: opts.meta }, "room.created");
  }

  joinRoom(roomId: string): Promise<Room> {
    return this.request("room.join", { room_id: roomId }, "room.joined");
  }

  leaveRoom(): Promise<void> {
    return this.request("room.leave", {}, "room.left").then(() => undefined);
  }

  listRooms(): Promise<Room[]> {
    return this.request<{ rooms: Room[] }>("room.list", {}, "room.list").then((r) => r.rooms);
  }

  /** Broadcast this player's state blob to everyone in the room. */
  sendState(state: Json): void {
    this.send("room.state", state);
  }

  /** Send an arbitrary payload to everyone else in the room. */
  sendRoomMessage(data: Json): void {
    this.send("room.msg", data);
  }

  subscribeChat(channel: string): Promise<void> {
    this.chatSubs.add(channel);
    return this.request("chat.sub", { channel }, "chat.subbed").then(() => undefined);
  }

  unsubscribeChat(channel: string): Promise<void> {
    this.chatSubs.delete(channel);
    return this.request("chat.unsub", { channel }, "chat.unsubbed").then(() => undefined);
  }

  sendChat(channel: string, content: string): void {
    this.send("chat.send", { channel, content });
  }
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

export class MiniCloud {
  readonly appId: string;
  readonly baseUrl: string;
  token?: string;

  readonly fetchImpl: typeof fetch;
  readonly webSocketImpl?: WebSocketConstructor;

  readonly auth: AuthAPI;
  readonly player: PlayerAPI;
  readonly playerData: PlayerDataAPI;
  readonly leaderboards: LeaderboardsAPI;
  readonly achievements: AchievementsAPI;
  readonly friends: FriendsAPI;
  readonly currency: CurrencyAPI;
  readonly mailbox: MailboxAPI;
  readonly announcements: AnnouncementsAPI;
  readonly functions: FunctionsAPI;
  readonly updates: UpdatesAPI;
  readonly dialogues: DialoguesAPI;
  readonly chat: ChatAPI;
  readonly kv: KVAPI;
  readonly logs: LogsAPI;
  readonly realtime: RealtimeClient;

  constructor(options: MiniCloudOptions) {
    if (!options.appId) throw new MiniCloudError(0, "bad_options", "appId is required");
    if (!options.baseUrl) throw new MiniCloudError(0, "bad_options", "baseUrl is required");
    this.appId = options.appId;
    this.baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.token = options.token;
    const rawFetch = options.fetch ?? globalThis.fetch;
    if (!rawFetch) throw new MiniCloudError(0, "no_fetch", "no fetch available; pass one via options.fetch");
    this.fetchImpl = rawFetch.bind(globalThis);
    this.webSocketImpl =
      options.webSocket ?? (globalThis as { WebSocket?: WebSocketConstructor }).WebSocket;

    const http = new Http(this);
    this.auth = new AuthAPI(this, http);
    this.player = new PlayerAPI(http);
    this.playerData = new PlayerDataAPI(http);
    this.leaderboards = new LeaderboardsAPI(http);
    this.achievements = new AchievementsAPI(http);
    this.friends = new FriendsAPI(http);
    this.currency = new CurrencyAPI(http);
    this.mailbox = new MailboxAPI(http);
    this.announcements = new AnnouncementsAPI(this, http);
    this.functions = new FunctionsAPI(this, http);
    this.updates = new UpdatesAPI(this, http);
    this.dialogues = new DialoguesAPI(http);
    this.chat = new ChatAPI(http);
    this.kv = new KVAPI(http);
    this.logs = new LogsAPI(http);
    this.realtime = new RealtimeClient(this);
  }
}

export default MiniCloud;
