/**
 * 游戏状态：服务端权威快照的本地镜像。
 * 原则：本地状态只是表现缓存，任何冲突以服务端 state_snapshot 为准。
 */
import { StateSnapshot, BattleReport, IdleResult } from './Protocol';

export class GameState {
  private _snapshot: StateSnapshot | null = null;
  private _bd: string[] = [];
  private _report: BattleReport | null = null;
  private _idle: IdleResult | null = null;
  onChange?: () => void;

  get snapshot(): StateSnapshot | null { return this._snapshot; }
  get bd(): string[] { return this._bd; }
  get report(): BattleReport | null { return this._report; }

  /** 应用服务端快照（登录/重连/战斗结算后） */
  applySnapshot(s: StateSnapshot): void {
    this._snapshot = s;
    this.onChange?.();
  }

  setBD(slots: string[]): void {
    this._bd = slots.slice(0, 4);
  }

  /** 战斗结算：服务端战报只做表现，属性变化等快照下发后再 apply */
  applyReport(r: BattleReport): void {
    this._report = r;
    this.onChange?.();
  }

  applyIdle(r: IdleResult): void { this._idle = r; this.onChange?.(); }
}
