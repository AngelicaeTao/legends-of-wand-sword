/**
 * look.js — 家园外观 6 部件合成（交付包 §2.2 / preview_creator_combo.py 的运行时实现）
 *
 * 叠放顺序：face → hair → hat → top → pants → shoes（creator_manifest.json 的 overlay_order）
 * 人体布局（逻辑 px，y 向上，脚底 y=0）：鞋 0 / 裤 8 / 上衣 38 / 脸 62 / 发 62 / 帽 86
 * 每部件按目标高度缩放后水平居中——face 与 hair 用 grid 模式切分（同为 56×74），坐标系一致、
 * 中心天然对齐，发型罩头顶而不挡五官。
 *
 * 因为部件图带透明边（尤其 face/hair 的 74 高盒子里只有 ~60 的头部），直接按盒子画会出现
 * "人物比画布小一圈、上方大片空白"。这里按各部件真实 alpha 包围盒求合成包围盒，
 * 再用 drawFit 按目标高度自动取景（预览/家园头像都能填满且不变形）。
 */
(function (global) {
  'use strict';

  const BASE = '/res/characters/creator_parts/';
  const PLACE = { shoes: [0, 0], pants: [0, 8], top: [0, 38], face: [0, 62], hair: [0, 62], hat: [0, 86] };
  const TARGETH = { shoes: 10, pants: 32, top: 30, face: 30, hair: 30, hat: 14 };
  const ORDER = ['face', 'hair', 'hat', 'top', 'pants', 'shoes'];

  const imgs = new Map();   // file -> Image
  const metas = new Map();  // Image -> {x0,y0,x1,y1}（alpha 包围盒，0..1 比例）
  let manifest = null;

  function setManifest(m) { manifest = m; }
  function getManifest() { return manifest; }

  function optOf(slot, id) {
    if (!manifest || !manifest.slots || !manifest.slots[slot]) return null;
    return (manifest.slots[slot].options || []).find(o => o.id === id) || null;
  }

  function imgOf(file) {
    if (!file) return null;
    if (imgs.has(file)) return imgs.get(file);
    const im = new Image();
    im._ready = false;
    im.onload = () => { im._ready = true; };
    im.src = BASE + file + '?v=1.9.96';
    imgs.set(file, im);
    return im;
  }

  function metaOf(im) {
    if (metas.has(im)) return metas.get(im);
    let m = { x0: 0, y0: 0, x1: 1, y1: 1 };
    try {
      const c = document.createElement('canvas');
      c.width = im.naturalWidth; c.height = im.naturalHeight;
      const g = c.getContext('2d', { willReadFrequently: true });
      g.drawImage(im, 0, 0);
      const d = g.getImageData(0, 0, c.width, c.height).data;
      let px0 = c.width, py0 = c.height, px1 = -1, py1 = -1;
      for (let y = 0; y < c.height; y++) {
        for (let x = 0; x < c.width; x++) {
          if (d[(y * c.width + x) * 4 + 3] > 12) {
            if (x < px0) px0 = x;
            if (x > px1) px1 = x;
            if (y < py0) py0 = y;
            if (y > py1) py1 = y;
          }
        }
      }
      if (px1 >= px0 && py1 >= py0) {
        m = { x0: px0 / c.width, y0: py0 / c.height, x1: (px1 + 1) / c.width, y1: (py1 + 1) / c.height };
      }
    } catch (e) { /* 跨域/画布异常：退回整图盒 */ }
    metas.set(im, m);
    return m;
  }

  /** 预加载全部部件 */
  function preloadAll() {
    if (!manifest || !manifest.slots) return Promise.resolve();
    const jobs = [];
    for (const slot in manifest.slots) {
      for (const o of (manifest.slots[slot].options || [])) {
        const im = imgOf(o.file);
        if (im && !im._ready) {
          jobs.push(new Promise(res => {
            im.addEventListener('load', res, { once: true });
            im.addEventListener('error', res, { once: true });
          }));
        }
      }
    }
    return Promise.all(jobs);
  }

  /** 合成包围盒（逻辑 px，y 向上，脚底=0） */
  function bbox(look) {
    if (!manifest || !look) return null;
    let x0 = 1e9, x1 = -1e9, y0 = 1e9, y1 = -1e9;
    for (const slot of ORDER) {
      const id = look[slot];
      if (!id || id === 'none') continue;
      const o = optOf(slot, id);
      if (!o) continue;
      const im = imgOf(o.file);
      if (!im || !im._ready) continue;
      const H = TARGETH[slot] || 20;
      const W = im.naturalWidth / im.naturalHeight * H;
      const m = metaOf(im);
      const base = (PLACE[slot] || [0, 0])[1];
      x0 = Math.min(x0, -W / 2 + W * m.x0);
      x1 = Math.max(x1, -W / 2 + W * m.x1);
      y0 = Math.min(y0, base + H * (1 - m.y1));
      y1 = Math.max(y1, base + H * (1 - m.y0));
    }
    if (x1 < x0 || y1 < y0) return null;
    return { x0, x1, y0, y1, w: x1 - x0, h: y1 - y0 };
  }

  /**
   * 以逻辑原点（脚底中心）为基准绘制。
   * @param {number} ox 逻辑原点 x（屏幕 px）
   * @param {number} oy 逻辑原点 y（屏幕 px，= 脚底）
   * @param {number} s  逻辑→屏幕倍率（像素图请传整数，避免重采样发糊）
   */
  function draw(ctx, ox, oy, s, look) {
    if (!manifest || !look) return false;
    let drew = false;
    ctx.save();
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = 'high';
    for (const slot of ORDER) {
      const id = look[slot];
      if (!id || id === 'none') continue;
      const o = optOf(slot, id);
      if (!o) continue;
      const im = imgOf(o.file);
      if (!im || !im._ready) continue;
      const H = (TARGETH[slot] || 20) * s;
      const W = im.naturalWidth / im.naturalHeight * H;
      const x = ox - W / 2;
      const y = oy - ((PLACE[slot] ? PLACE[slot][1] : 0) + TARGETH[slot]) * s;
      ctx.drawImage(im, x, y, W, H);
      drew = true;
    }
    ctx.restore();
    return drew;
  }

  /**
   * 按最大高度自动取景绘制（脚底对齐 footY，水平居中于 cx）。
   *
   * 像素图必须整数倍缩放，否则浏览器重采样会把人像糊成一团（v1.9.5 预览就是 3.6 倍导致发虚）。
   * 这里先按 maxH 求理想倍率，再向下取整到整数倍；取整后若画面偏小（不足 maxH 的 70%）
   * 且只差一点，就向上取一档，尽量填满预览区。
   */
  function drawFit(ctx, cx, footY, maxH, look) {
    const bb = bbox(look);
    if (!bb) return false;
    const raw = maxH / bb.h;
    let s = Math.max(1, Math.floor(raw));
    if (s < raw && (s + 1) * bb.h <= maxH * 1.02) s += 1;   // 只差一点点就补一档
    const ox = cx - (bb.x0 + bb.x1) / 2 * s;
    const oy = footY + bb.y0 * s;
    return draw(ctx, ox, oy, s, look);
  }

  /**
   * 画进一个 canvas（家园头像用），按画布高度自动取景。
   * @param {number} [scaleLimit] 可选：倍率上限（小画布上避免过大的整数倍）
   */
  function renderInto(canvas, look, pad, scaleLimit) {
    if (!canvas) return false;
    const g = canvas.getContext('2d');
    g.clearRect(0, 0, canvas.width, canvas.height);
    const p = pad == null ? 12 : pad;
    const maxH = (canvas.height - p * 2) * (scaleLimit || 1);
    return drawFit(g, canvas.width / 2, canvas.height - p, maxH, look);
  }

  function partSrc(slot, id) {
    const o = optOf(slot, id);
    return o ? BASE + o.file : '';
  }

  /** 当前搭配的文字摘要（家园/命名页共用） */
  function describe(look) {
    return ORDER.map(k => {
      if (!look || !look[k]) return '';
      if (look[k] === 'none') return '不戴帽';
      const o = optOf(k, look[k]);
      return o ? o.name : '';
    }).filter(Boolean).join(' · ');
  }

  global.Look = { ORDER, PLACE, TARGETH, setManifest, getManifest, optOf, bbox, draw, drawFit, renderInto, preloadAll, partSrc, describe, BASE };
})(window);
