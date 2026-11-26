/**
 * 网络管理器：WebSocket/WSS 单一连接、心跳保活、退避重连、消息分派。
 * 隧道地址从配置读取，绝不硬编码（地址每次启动会变）。
 * 重连后以服务端快照为准，不信任客户端本地缓存。
 */
import { Envelope, envelope, MsgType, PROTOCOL_VERSION } from './Protocol';

type Handler = (data: any, env: Envelope) => void;

export interface NetOptions {
  host: string;       // 如 abc-xyz.trycloudflare.com
  port: number;       // 443 (WSS) 或 7350 (本地 WS)
  useSSL: boolean;
  serverKey: string;  // 从 connection_info.json 读，不硬编码
  heartbeatMs?: number;
  backoffMs?: number[];
}

export class NetworkManager {
  private ws: WebSocket | null = null;
  private seq = 0;
  private handlers = new Map<MsgType, Handler>();
  private pending = new Map<number, { resolve: Function; reject: Function }>();
  private reconnectIdx = 0;
  private hbTimer: any = null;
  private closedByUser = false;
  onStateChange?: (state: 'connecting' | 'open' | 'closed' | 'reconnecting') => void;

  constructor(private opt: NetOptions) {}

  private get url(): string {
    const scheme = this.opt.useSSL ? 'wss' : 'ws';
    // Nakama 实时端点格式：/ws?key=<serverKey>
    return `${scheme}://${this.opt.host}:${this.opt.port}/ws?key=${encodeURIComponent(this.opt.serverKey)}`;
  }

  connect(): void {
    this.closedByUser = false;
    this.onStateChange?.('connecting');
    this.ws = new WebSocket(this.url);
    this.ws.onopen = () => {
      this.onStateChange?.('open');
      this.reconnectIdx = 0;
      this.startHeartbeat();
    };
    this.ws.onmessage = (ev) => this.onMessage(ev.data);
    this.ws.onclose = () => {
      this.stopHeartbeat();
      if (!this.closedByUser) this.reconnect();
    };
    this.ws.onerror = () => { this.ws?.close(); };
  }

  private onMessage(raw: string): void {
    let env: Envelope;
    try { env = JSON.parse(raw); } catch { return; }
    if (env.v !== PROTOCOL_VERSION) return; // 版本不匹配丢弃，向前兼容
    if (env.seq && this.pending.has(env.seq)) {
      const p = this.pending.get(env.seq)!;
      this.pending.delete(env.seq);
      env.code ? p.reject(env) : p.resolve(env.data);
    }
    this.handlers.get(env.type)?.(env.data, env);
  }

  /** 注册服务端主动推送的处理器（如 battle_report / state_snapshot） */
  on(type: MsgType, fn: Handler): void { this.handlers.set(type, fn); }

  /** 请求-响应式发送：返回 Promise，按 seq 匹配回包 */
  request(type: MsgType, data: any): Promise<any> {
    return new Promise((resolve, reject) => {
      if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
        reject(new Error('连接未就绪')); return;
      }
      const seq = ++this.seq;
      this.pending.set(seq, { resolve, reject });
      this.ws.send(JSON.stringify(envelope(type, data, seq)));
    });
  }

  /** 单向发送（如 move 同步，不等回包） */
  send(type: MsgType, data: any): void {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(envelope(type, data, ++this.seq)));
    }
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    const ms = this.opt.heartbeatMs ?? 5000;
    this.hbTimer = setInterval(() => this.send('heartbeat', { ts: Date.now() }), ms);
  }
  private stopHeartbeat(): void { if (this.hbTimer) clearInterval(this.hbTimer); }

  private reconnect(): void {
    this.onStateChange?.('reconnecting');
    const list = this.opt.backoffMs ?? [1000, 2000, 4000, 8000];
    const delay = list[Math.min(this.reconnectIdx, list.length - 1)];
    this.reconnectIdx++;
    setTimeout(() => this.connect(), delay);
  }

  close(): void {
    this.closedByUser = true;
    this.stopHeartbeat();
    this.ws?.close();
  }
}
