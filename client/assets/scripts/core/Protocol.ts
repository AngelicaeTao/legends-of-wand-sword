/**
 * 通信协议：客户端 ↔ 服务端（与 configs 无关的强类型契约）。
 * 所有消息带 type / seq / v 三字段，请求响应成对，错误带 code + msg。
 * 与服务端 internal/game/*.go 的结构体字段一一对应。
 */

export const PROTOCOL_VERSION = 1;

export type MsgType =
  | 'login' | 'submit_bd' | 'engage' | 'claim_idle'
  | 'match_req' | 'join_world' | 'match_join' | 'move' | 'heartbeat'
  | 'battle_report' | 'state_snapshot' | 'player_move'
  | 'world_snapshot' | 'player_join' | 'player_leave'
  | 'pong' | 'error';

/** C→S join_world：按 worldId 取/建一条线，返回 matchId（走 RPC） */
export interface JoinWorldReq { worldId: string }
export interface JoinWorldResp { matchId: string; worldId: string }

export interface Envelope {
  type: MsgType;
  seq: number;
  v: number;
  data?: any;
  code?: number;
  msg?: string;
}

/** C→S login：设备登录，零输入 */
export interface LoginReq { deviceId: string; username?: string; password?: string }

/** C→S submit_bd：战前技能配置（战斗中不生效） */
export interface SubmitBDReq { slots: string[]; }

/** C→S engage：请求交战，服务端起权威战斗 */
export interface EngageReq {
  monsterId: string;
  monsterLv: number;
  elite?: '' | 'elite' | 'rareElite';
  seed?: number; // >0 复现用
}

/** S→C battle_report：客户端只做表现，不参与结算 */
export interface BattleReport {
  seed: number;
  result: 'win' | 'lose';
  rounds: BattleRound[];
  exp: number;
  gold: number;
  drops: EquipDrop[];
  seconds: number;
  log: string;
}
export interface BattleRound {
  n: number;
  actor: 'hero' | 'monster';
  action: string;
  skillName: string;
  fx: string;
  damage: number;
  heal: number;
  heroHp: number;
  monHp: number;
}
export interface EquipDrop {
  id: string; slot: string; quality: number; enhance: number;
  stats: Record<string, number>;
}

/** S→C state_snapshot：登录/重连后的服务端权威快照 */
export interface StateSnapshot {
  level: number; exp: number; battlePower: number; classId: string;
  gold: number; gem: number; stamina: number;
  worldId: string; x: number; y: number; unlocked: string[]; lastSeen: number;
}

/** C→S claim_idle：结算离线收益 */
export interface IdleResult {
  minutes: number; hours: number; capped: boolean;
  exp: number; gold: number; material: number; log: string;
}

/** C→S move / S→C player_move：多人位置同步 */
export interface MoveMsg { x: number; y: number; ts: number; action?: 'idle' | 'walk' | 'battle' }
export interface PlayerMove extends MoveMsg { playerId: string }

export function envelope(type: MsgType, data: any, seq: number): Envelope {
  return { type, seq, v: PROTOCOL_VERSION, data };
}
