/**
 * 配置管理器：加载 configs/*.json，全部数值/职业/技能/装备/地图从这里读。
 * 可灵活修改的关键：改 JSON 不改代码；支持运行时 reload。
 * Cocos 用法：把 configs 放到 assets/resources/configs，用 resources.load 加载。
 */
import { PROTOCOL_VERSION } from './Protocol';

export interface ConfigBundle {
  classes: any;
  skills: any;
  equipment: any;
  monsters: any;
  economy: any;
  worlds: any;
}

export class ConfigManager {
  private static _inst: ConfigManager;
  static get instance(): ConfigManager {
    if (!this._inst) this._inst = new ConfigManager();
    return this._inst;
  }

  private cfg: ConfigBundle | null = null;
  private listeners: Array<(c: ConfigBundle) => void> = [];

  /** 从服务端 HTTP 拉取（初跑 Web 构建）或从本地 resources 加载（Cocos 原生构建） */
  async loadFromServer(baseUrl: string): Promise<void> {
    const res = await fetch(baseUrl + '/api/config');
    if (!res.ok) throw new Error('配置加载失败: ' + res.status);
    this.set(await res.json());
  }

  set(bundle: ConfigBundle): void {
    this.cfg = bundle;
    this.listeners.forEach(fn => fn(bundle));
  }

  get(): ConfigBundle {
    if (!this.cfg) throw new Error('配置未加载，请先调用 loadFromServer / set');
    return this.cfg;
  }

  /** 配置热更新监听：数值调整后 UI 自动刷新 */
  onChange(fn: (c: ConfigBundle) => void): void { this.listeners.push(fn); }

  classById(id: string): any { return this.get().classes.classes.find((c: any) => c.id === id); }
  skillById(id: string): any { return this.get().skills.skills.find((s: any) => s.id === id); }
  monsterById(id: string): any { return this.get().monsters.monsters.find((m: any) => m.id === id); }
  worldById(id: string): any { return this.get().worlds.worlds.find((w: any) => w.id === id); }

  /** 逻辑分辨率基准（修订说明 R-01：统一为 1536×1024） */
  logicSize(): { w: number; h: number } { return this.get().economy.rendering.logicSize; }
}
