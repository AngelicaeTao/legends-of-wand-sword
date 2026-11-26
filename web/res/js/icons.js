/* ============================================================
   杖剑传说 · 内联图标库（v1.8）
   ------------------------------------------------------------
   为什么不用 emoji：Windows/Android/Linux 字体对 emoji 的覆盖不一致，
   🗡️🪖🛡️💍🔒 等在部分设备上渲染成方框或错误字形（实测安卓与 headless
   Chromium 都会缺字）。这里统一用内联 SVG：任何设备一致、随色、可缩放、零请求。
   用法：ICON.sword(20, '#f0d590')  →  SVG 字符串
   ============================================================ */
(function(){
'use strict';
const DARK='#3b2600', WOOD='#8a5a2b', LIGHT='#fff3c4';
function wrap(body,size,color){
  const s=String(body).split('COL').join(color||'#f0d590');
  return `<svg viewBox="0 0 24 24" width="${size||20}" height="${size||20}" aria-hidden="true" ` +
         `style="display:inline-block;vertical-align:middle;flex:0 0 auto">${s}</svg>`;
}
const I={
  sword:(s,c)=>wrap(`<path d="M18.5 2.5 L21.5 5.5 L11 16 L8 13 Z" fill="COL"/>
     <path d="M6.2 14.6 L9.4 17.8" stroke="${DARK}" stroke-width="2.4" stroke-linecap="round"/>
     <path d="M7 17.4 L3 21.5" stroke="${WOOD}" stroke-width="2.6" stroke-linecap="round"/>`,s,c),
  helmet:(s,c)=>wrap(`<path d="M4 13a8 8 0 0 1 16 0v4.6H4z" fill="COL"/>
     <rect x="2.6" y="17" width="18.8" height="3.6" rx="1.4" fill="${DARK}" opacity=".55"/>
     <path d="M12 5v13" stroke="${DARK}" stroke-width="1.6" opacity=".45"/>`,s,c),
  armor:(s,c)=>wrap(`<path d="M8.6 3 L12 6 L15.4 3 L20 6.4 L17.4 9.6 V21 H6.6 V9.6 L4 6.4 Z" fill="COL"/>
     <path d="M12 8.6 V18" stroke="${DARK}" stroke-width="1.6" opacity=".45"/>`,s,c),
  ring:(s,c)=>wrap(`<circle cx="12" cy="15" r="5.2" fill="none" stroke="COL" stroke-width="3"/>
     <path d="M12 5.2 L15.6 8.8 H8.4 Z" fill="COL"/>`,s,c),
  gold:(s,c)=>wrap(`<ellipse cx="12" cy="17.4" rx="8.4" ry="3.4" fill="${DARK}" opacity=".75"/>
     <ellipse cx="12" cy="14.6" rx="8.4" ry="3.4" fill="COL"/>
     <ellipse cx="12" cy="14.2" rx="5" ry="1.8" fill="${LIGHT}" opacity=".7"/>`,s,c),
  gem:(s,c)=>wrap(`<path d="M7 4.6 H17 L21 10 L12 20.4 L3 10 Z" fill="COL"/>
     <path d="M3 10 H21 M7 4.6 L12 20.4 M17 4.6 L12 20.4" stroke="${DARK}" stroke-width="1.2" opacity=".35"/>`,s,c),
  bag:(s,c)=>wrap(`<path d="M6.2 9 H17.8 L20 21 H4 Z" fill="COL"/>
     <path d="M9 9 V6.4 a3 3 0 0 1 6 0 V9" fill="none" stroke="${DARK}" stroke-width="2.2"/>`,s,c),
  hourglass:(s,c)=>wrap(`<path d="M6 2.6 H18 V6 L12.8 12 L18 18 V21.4 H6 V18 L11.2 12 L6 6 Z" fill="COL"/>
     <rect x="5" y="1.6" width="14" height="2.4" rx="1" fill="${DARK}" opacity=".6"/>
     <rect x="5" y="20" width="14" height="2.4" rx="1" fill="${DARK}" opacity=".6"/>`,s,c),
  lock:(s,c)=>wrap(`<rect x="4.6" y="10" width="14.8" height="11" rx="2.2" fill="COL"/>
     <path d="M8 10 V7.2 a4 4 0 0 1 8 0 V10" fill="none" stroke="COL" stroke-width="2.6"/>`,s,c),
  chest:(s,c)=>wrap(`<rect x="2.6" y="8.6" width="18.8" height="11.8" rx="2" fill="COL"/>
     <path d="M2.6 12.6 H21.4" stroke="${DARK}" stroke-width="2" opacity=".6"/>
     <rect x="10.4" y="11.2" width="3.2" height="4.2" rx=".8" fill="${LIGHT}"/>`,s,c),
  map:(s,c)=>wrap(`<path d="M3 6.4 L9 4.2 L15 6.4 L21 4.2 V17.6 L15 19.8 L9 17.6 L3 19.8 Z" fill="COL"/>
     <path d="M9 4.2 V17.6 M15 6.4 V19.8" stroke="${DARK}" stroke-width="1.5" opacity=".45"/>`,s,c),
  shield:(s,c)=>wrap(`<path d="M12 2.6 L20 5.8 V12.6 c0 4.2-3.6 7.2-8 8.8 -4.4-1.6-8-4.6-8-8.8 V5.8 Z" fill="COL"/>
     <path d="M12 7 L15.4 13 H8.6 Z" fill="${DARK}" opacity=".45"/>`,s,c),
  hammer:(s,c)=>wrap(`<rect x="3.4" y="3.6" width="11" height="6.2" rx="1.4" fill="COL"/>
     <rect x="10.4" y="9.2" width="3.4" height="5.6" fill="${WOOD}"/>
     <rect x="7.6" y="14.4" width="9" height="2.6" rx="1.2" fill="COL"/>`,s,c),
  star:(s,c)=>wrap(`<path d="M12 2.4 L15 9 L22 9.8 L16.8 14.4 L18.3 21.2 L12 17.6 L5.7 21.2 L7.2 14.4 L2 9.8 L9 9 Z" fill="COL"/>`,s,c),
  flask:(s,c)=>wrap(`<path d="M9 2.8 H15 V7.2 L19.4 16.4 a3 3 0 0 1-2.7 4.4 H7.3 a3 3 0 0 1-2.7-4.4 L9 7.2 Z" fill="COL"/>
     <path d="M7.6 15.4 H16.4" stroke="${DARK}" stroke-width="1.6" opacity=".4"/>`,s,c),
  skull:(s,c)=>wrap(`<path d="M12 2.6 a8.4 8.4 0 0 0-8.4 8.4 c0 3 1.4 4.6 3 5.4 V20 h10.8 v-3.6 c1.6-.8 3-2.4 3-5.4 A8.4 8.4 0 0 0 12 2.6 Z" fill="COL"/>
     <circle cx="8.8" cy="11.4" r="2" fill="${DARK}"/><circle cx="15.2" cy="11.4" r="2" fill="${DARK}"/>`,s,c),
  bolt:(s,c)=>wrap(`<path d="M13.6 2 L4.6 14 H10.4 L9 22 L18.4 9.4 H12.4 Z" fill="COL"/>`,s,c),
  book:(s,c)=>wrap(`<path d="M3.4 4 H11 V20 H3.4 Z" fill="COL"/><path d="M13 4 H20.6 V20 H13 Z" fill="COL" opacity=".78"/>`,s,c),
  tent:(s,c)=>wrap(`<path d="M12 3.4 L21.4 20.6 H2.6 Z" fill="COL"/><path d="M12 3.4 V20.6" stroke="${DARK}" stroke-width="1.6" opacity=".5"/>`,s,c),
  flag:(s,c)=>wrap(`<rect x="5" y="2.4" width="2.6" height="19.2" rx="1.2" fill="${WOOD}"/>
     <path d="M7.6 3.6 H20 L16.4 8.2 L20 12.8 H7.6 Z" fill="COL"/>`,s,c),
  npc:(s,c)=>wrap(`<circle cx="12" cy="7.6" r="4" fill="COL"/><path d="M4 21 a8 8 0 0 1 16 0 Z" fill="COL"/>`,s,c),
  leaf:(s,c)=>wrap(`<path d="M20 3 C10 3 4 8 4 15 c0 2 .6 4 1.6 5 C7 12 13 8 19 7.4 14 11 9 13 6 20 c8 1 14-4 14-13 Z" fill="COL"/>`,s,c),
  crystal:(s,c)=>wrap(`<path d="M12 1.8 L17.4 8.6 L15 21.4 H9 L6.6 8.6 Z" fill="COL"/>
     <path d="M12 1.8 V21.4" stroke="${DARK}" stroke-width="1.4" opacity=".4"/>`,s,c),
  cart:(s,c)=>wrap(`<path d="M3 5.2 h3.4 l2.6 9.4 h9.4" fill="none" stroke="COL" stroke-width="2.4" stroke-linecap="round"/>
     <path d="M8.2 9.6 H21 L19.2 15.2 H9.6 Z" fill="COL"/>
     <circle cx="10.4" cy="19.4" r="2" fill="COL"/><circle cx="17.6" cy="19.4" r="2" fill="COL"/>`,s,c),
  bed:(s,c)=>wrap(`<rect x="2.6" y="10" width="18.8" height="7.4" rx="1.6" fill="COL"/>
     <rect x="2.6" y="7.2" width="5.4" height="4.4" rx="1.4" fill="COL" opacity=".85"/>
     <rect x="2.6" y="17.4" width="2.4" height="3" fill="${WOOD}"/><rect x="19" y="17.4" width="2.4" height="3" fill="${WOOD}"/>`,s,c),
  scroll:(s,c)=>wrap(`<path d="M5 4.6 h13 a2 2 0 0 1 0 4 v10.8 H6.4 a2 2 0 0 1 0-4 Z" fill="COL"/>
     <path d="M8.6 9.6 H16 M8.6 12.6 H16 M8.6 15.6 H13.6" stroke="${DARK}" stroke-width="1.4" opacity=".5"/>`,s,c)
};
window.ICON=I;
})();
