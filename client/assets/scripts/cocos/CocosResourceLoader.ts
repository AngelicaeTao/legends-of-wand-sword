/**
 * Cocos 资源加载适配：把纯逻辑层的 ConfigManager 接到引擎 resources 系统。
 * 核心逻辑层（core/systems）不 import 'cc'，保持可在纯 tsc / Web 下独立编译；
 * 本文件是唯一接触引擎 resources API 的桥。
 *
 * 加载顺序：connection_info.json → configs/*.json → animation_spec.json → 帧图片。
 */
import { resources, JsonAsset, SpriteFrame, AssetManager } from 'cc';
import { ConfigManager, ConfigBundle } from '../core/ConfigManager';
import { ConnectionInfo } from '../core/Bootstrap';

const CFG_FILES = ['classes', 'skills', 'equipment', 'monsters', 'economy', 'worlds'];

function loadJson(path: string): Promise<any> {
  return new Promise((resolve, reject) => {
    resources.load(path, JsonAsset, (err, asset) => {
      if (err || !asset) return reject(err || new Error('加载失败:' + path));
      resolve(asset.json);
    });
  });
}

/** 加载连接配置（发布时由联机脚本覆盖 connection_info.json） */
export async function loadConnectionInfo(): Promise<ConnectionInfo> {
  const j = await loadJson('connection_info');
  return {
    host: j.host, port: j.port, useSSL: !!j.useSSL,
    serverKey: j.serverKey, httpBase: j.httpBase || '',
  };
}

/** 从本地 resources 加载配置并注入 ConfigManager（原生构建路径） */
export async function loadConfigsLocal(): Promise<void> {
  const parts = await Promise.all(CFG_FILES.map(f => loadJson('configs/' + f)));
  const bundle: ConfigBundle = {
    classes: parts[0], skills: parts[1], equipment: parts[2],
    monsters: parts[3], economy: parts[4], worlds: parts[5],
  };
  ConfigManager.instance.set(bundle);
}

/** 加载一张 SpriteFrame（帧路径相对 resources，不含扩展名） */
export function loadFrame(pathNoExt: string): Promise<SpriteFrame> {
  return new Promise((resolve, reject) => {
    resources.load(pathNoExt, SpriteFrame, (err, frame) => {
      if (err || !frame) return reject(err || new Error('帧缺失:' + pathNoExt));
      resolve(frame);
    });
  });
}

/** 释放 resources（切场景时调用，避免泄漏） */
export function releaseResources(): void {
  resources.releaseAll();
}
