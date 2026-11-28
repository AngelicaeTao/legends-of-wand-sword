/* ============================================================
   杖剑传说 · 图形界面逻辑（客户端零游戏逻辑）
   ------------------------------------------------------------
   严格单通道导航（不可串界面）：
     [登录页] --进入游戏(唯一入口)--> [大世界枢纽]
     [大世界] --右上4按钮--> [角色|仓库|地图|挂机 面板] --返回(唯一出口)--> [大世界]
     [大世界] --遇怪/交战--> [战斗] --结算--> [大世界]
     [任意处] --退出登录(右下角)--> [登录页]
   守卫规则：
     - 面板只能从 world 打开；面板内不允许打开其他面板（必须返回 world 再进）
     - 战斗/面板期间右上4按钮不可用
     - 弹窗必须经明确按钮关闭，关闭后回到来处
   所有数值来自服务端权威 API，前端只做表现。
   ============================================================ */
(() => {
'use strict';
const RES='/res/';
const API = location.protocol==='file:' ? 'http://127.0.0.1:8080' : '';
const $ = id => document.getElementById(id);
const sleep = ms => new Promise(r=>setTimeout(r,ms));

/* ---------- 全局状态 ---------- */
let CFG=null, state=null;
let view='login';          // login | world | battle （唯一的顶层视图）
let panel=null;            // null | role | home | dungeon | guild | map （只能挂在 world 上）
let homeSub=null;          // 家园二级：null | warehouse | idle
let roleTab='info';        // 角色面板内 tab：info | equip | skill | class
let equipDetailSlot=null;  // 仓库内展开的装备详情部位
let forgeSub=false;        // v1.9.21 强化工坊子层开关
let forgeSel=null;         // 强化工坊当前选中的装备实例 id
let whBatch=null;          // v1.9.50 仓库批量模式：null | 'in'（批量存入）| 'out'（批量取出）| 'dec'（批量分解）
let whBatchSel=new Set();  // v1.9.50 批量选中项 key 集合：eq:<实例id> / mat:<物品id>
function clearWhBatch(){ whBatch=null; whBatchSel.clear(); }
let loggedUser=null;       // 当前登录账号（用户名）
let authToken=null;        // 服务端签发的 token（跨设备续玩凭据）
let authUserId=null;       // 服务端 userId
let deviceId=null;         // 本机设备号
let currentMonster=null, currentReport=null;
const MON_NAMES={slime:'史莱姆',skeleton:'骷髅兵',bat:'蝙蝠',boss_beetle:'甲虫将军',
  slime_toxic:'毒液史莱姆',skeleton_archer:'骷髅弓手',bat_crimson:'血蝙蝠',map_boss:'图域首领'}; // v1.9.37 新增衍生怪
let battlePlaying=false, paused=false;
let heroStats=null, pendingStats=null;   // v1.9.60 登录时服务端随存档一起下发的属性，供 finishEnter 复用        // 服务端权威属性（/api/stats/calc），前端不自行推算
let ws=null, clientId=null, onlineCount=1;
let wsChannel=0, wsRetry=0, wsTimers=[];   // 分线号 + 断线退避重连（1/2/4/8s）

/* ---------- 账号体系（服务端 Nakama 语义，token 持久化跨设备续玩） ---------- */
const SESSION_KEY='zjcs_session_v2', DEVICE_KEY='zjcs_device_id';
function getDeviceId(){
  if(deviceId)return deviceId;
  try{ deviceId=localStorage.getItem(DEVICE_KEY); }catch(e){}
  if(!deviceId){ deviceId='dev_'+Date.now().toString(36)+'_'+Math.random().toString(36).slice(2,8); try{localStorage.setItem(DEVICE_KEY,deviceId);}catch(e){} }
  return deviceId;
}
function restoreSession(){
  try{
    const s=JSON.parse(localStorage.getItem(SESSION_KEY)||'null');
    if(s && s.token && s.userId){ authToken=s.token; authUserId=s.userId; loggedUser=s.username||s.userId; return true; }
  }catch(e){}
  authToken=null;authUserId=null;loggedUser=null;return false;
}
function setSession(token,userId,username){
  authToken=token; authUserId=userId; loggedUser=username||userId;
  if(token) try{localStorage.setItem(SESSION_KEY,JSON.stringify({token,userId,username}));}catch(e){}
  else try{localStorage.removeItem(SESSION_KEY);}catch(e){}
}
const uid = () => authUserId || ('web_'+(loggedUser||'hero'));

/* ---------- 网络（自动携带 token；401 时提示重新登录） ---------- */
async function api(path, body, method){
  // v1.9.22：ngrok 免费版要求请求带 ngrok-skip-browser-warning 头，否则会被确认页拦截
  const headers={'Content-Type':'application/json','ngrok-skip-browser-warning':'1'};
  if(authToken) headers['Authorization']='Bearer '+authToken;
  const opt = {method: method||(body?'POST':'GET'), headers};
  if(body) opt.body=JSON.stringify(body);
  // 同时在 query 带 token：部分网关会剥离 Authorization 头，服务端 authUserID 支持 ?token= 兜底
  let url=API+path;
  if(authToken) url+=(url.indexOf('?')>=0?'&':'?')+'token='+encodeURIComponent(authToken);
  const r = await fetch(url, opt);
  if(!r.ok){ let msg='HTTP '+r.status; try{const txt=await r.text(); try{const e=JSON.parse(txt);msg=e.error||e.message||msg;}catch(e){ if(txt&&txt.trim())msg=txt.trim(); }}catch(e){} throw new Error(msg); }
  return r.json();
}

/* ---------- 资源预加载 ---------- */
const IMG={};
function loadImg(src){return new Promise(res=>{const im=new Image();im.onload=()=>res(im);im.onerror=()=>res(null);im.src=src;});}
async function preload(){
  // v3.0 部件动画：角色/怪物/宝箱由 PartActor 按需加载，这里只预载 UI 卡/图标/背景
  const jobs=[], add=(p,key)=>jobs.push(loadImg(p).then(im=>{IMG[key]=im;}));
  for(const b of ['grassland','forest','desert','snow','cave']) add(`${RES}v3/backgrounds/bg_explore_${b}.png`,'bg_'+b);
  add(`${RES}v3/backgrounds/bg_battle_arena.png`,'bg_arena');
  for(const c of ['warrior','mage','fighter','knight','sage','warlock']) add(`${RES}v3/characters/cards/ui_card_${c}.png`,'card_'+c);
  for(const ic of ['bag','coin','guild','hero','map','potion','quest','skill','storage','sword']) add(`${RES}v3/icons/icon_${ic}.png`,'icon_'+ic);
  // 预载部件动画 bundle（角色+怪物+宝箱）
  const parts=['char_hero_mage','mon_slime','mon_skeleton','mon_bat','mon_boss_beetle','item_chest_wood','item_chest_silver','item_chest_gold','item_chest_diamond','item_chest_crystal'];
  jobs.push(window.PartActor.preload(parts));
  let done=0; const total=jobs.length;
  await Promise.all(jobs.map(j=>Promise.resolve(j).then(()=>{$('loadBar').style.width=Math.round(++done/total*100)+'%';})));
  $('loadTxt').textContent='就绪';
}

/* ---------- 视口缩放 ---------- */
function fitStage(){
  const s=Math.min(innerWidth/1280, innerHeight/720);
  $('stage').style.transform=`translate(-50%,-50%) scale(${s})`;
}
addEventListener('resize',fitStage); fitStage();

/* ============================================================
   Canvas 渲染
   ============================================================ */
const cvb=$('cvBattle').getContext('2d');
cvb.imageSmoothingEnabled=false;

/* 俯视大地图引擎：回调注入到本模块的状态机 */
const worldMap=new WorldMap($('cvExplore'),{
  onEncounter:(mon)=>askEncounter(mon),          // 点怪物 → 交战确认
  onChest:(chest)=>onChestOpened(chest),         // 开箱动画开始 → 服务端权威入账
  onPotion:(p)=>onPotionPicked(p),               // v1.9.27 采集草药 → 服务端权威入仓
  onGate:(gate)=>askTravel(gate),                // 点传送门 → 世界切换确认（家园大门 to=BACK）
  onPlayer:(p)=>askPlayerProfile(p),             // v1.9.42 点其他玩家 → 查看档案
  onOnlineList:()=>showOnlinePlayers(),          // v1.9.42 大地图 → 在线玩家列表
  onHomeItem:(h)=>openHomeFunc(h),               // v1.9.31 家园功能点（仓库/挂机修炼/换装镜）
  onHint:(msg)=>{ const el=$('worldHintText'); if(el){ el.textContent=msg; clearTimeout(el._t); el._t=setTimeout(()=>{el.textContent='WASD/方向键移动 · 点怪交战 · 点宝箱开启 · 点⇄传送';},2200);} },
  onTile:(k)=>{ /* 记录探索格子 */ }
});
window.__wm=worldMap; // 调试钩子：便于自动化读取实体坐标（不影响玩法）
// 调试钩子：战斗倒地状态（自动化验证"死亡后不会自己站起来"）
window.__bt=()=>{const a=battleHeroActor;return {b:bAnim.hero,state:a?a.state:null,sy:a&&a.cur&&a.cur.root?+a.cur.root.sy.toFixed(3):null,
  modal:!document.getElementById('modalLayer').classList.contains('hidden'),
  modalTxt:document.getElementById('modalLayer').innerText.replace(/\n/g,'|').slice(0,40)};};
const miniCtx=$('miniMapCv').getContext('2d');

// v1.9.64 点小地图直接打开大地图（原来只能按 M 或点底部「地图」入口）
const miniCvEl=$('miniMapCv');
miniCvEl.title='点击打开大地图';
miniCvEl.addEventListener('click',(e)=>{
  // 只响应圆盘内：canvas 是 180×180 方形，四角在圆外，点到角落不该开图
  const r=miniCvEl.getBoundingClientRect(); if(!r.width)return;
  const cx=(e.clientX-r.left)*(MM_S/r.width), cy=(e.clientY-r.top)*(MM_S/r.height);
  if(Math.hypot(cx-MM_S/2,cy-MM_S/2)>MM_S/2-2)return;
  // 与 M 键同一守卫：仅大世界态（战斗中/面板打开时 active=false）
  if(worldMap&&worldMap.active)worldMap.openMapModal();
});

// v1.9.64 小地图下方旗标条：跟踪状态 + 距离，点一下打开大地图（在那里可增删标记）
// v1.9.82 多标记点：条上只放摘要（个数 + 最近的那个的距离），逐个管理去大地图列表
const mmPinEl=$('mmPin'), mmPinTxt=$('mmPinTxt');
let _mmPinText='';
function refreshMmPin(){
  if(!mmPinEl||!mmPinTxt)return;
  const wp=(worldMap&&worldMap.worldPins)?worldMap.worldPins():[];
  if(!wp.length){
    if(!mmPinEl.classList.contains('hidden'))mmPinEl.classList.add('hidden');
    _mmPinText=''; return;
  }
  mmPinEl.classList.remove('hidden');
  const near=worldMap.curPin();
  const nTrack=wp.filter(p=>p.track).length;
  mmPinEl.classList.toggle('off',nTrack===0);
  const d=worldMap.pinDistOf(near);
  const head=wp.length>1?('标记 '+wp.length+' · '):(nTrack?'跟踪 ':'标记 ');
  const s=head+(d>0?d+' 格':'附近');
  if(s!==_mmPinText){
    mmPinTxt.textContent=s;
    mmPinEl.title=wp.length>1?('共 '+wp.length+' 个标记点（'+nTrack+' 个跟踪中）· 点击打开大地图管理'):'点击打开大地图管理标记';
    _mmPinText=s;
  }
}
if(mmPinEl)mmPinEl.addEventListener('click',()=>{
  if(!worldMap)return;
  if(worldMap.active)worldMap.openMapModal();      // 与 M 键同一守卫：非大世界态不开图
});

// 小地图：真实雷达（以角色为中心，与通行掩码同源，北向上）
// 旧版是写死的装饰底图（假水域/假十字路），与实际路网无关 → 位置"对不上"
const MM_S=180, MM_RANGE=2048;              // 显示直径 2048 世界单位（约 1.6 屏）
const MM_K=MM_S/MM_RANGE;                   // 世界单位 → 小地图像素
function drawMiniMap(){
  if(!worldMap.ready)return;
  const g=miniCtx, S=MM_S, C=S/2, R=C-2;
  const hx=worldMap.hero.x, hy=worldMap.hero.y;
  g.clearRect(0,0,S,S);
  g.save();
  g.beginPath();g.arc(C,C,R,0,Math.PI*2);g.clip();
  g.fillStyle='#0f1a12';g.fillRect(0,0,S,S);  // 世界外底色
  // ---- 底图：优先真实地形缩略（与主画面同一张图），失败用掩码上色兜底 ----
  const MC=WorldMapConst||{};
  let img=null,SC=0,IW=0,IH=0;
  if(worldMap.thumb&&MC.THUMB_W){img=worldMap.thumb;SC=(worldMap.thumbScale||MC.THUMB_SCALE);IW=(worldMap.thumbW||MC.THUMB_W);IH=(worldMap.thumbH||MC.THUMB_H);}
  else if(worldMap.miniCv&&MC.MINI_W){img=worldMap.miniCv;SC=MC.MINI_SCALE;IW=MC.MINI_W;IH=MC.MINI_H;}
  if(img){
    const span=MM_RANGE/SC, k=S/span;        // 源 span px → 目标 180px
    let sx=hx/SC-span/2, sy=hy/SC-span/2, sw=span, sh=span, dx=0, dy=0;
    if(sx<0){dx=-sx*k;sw+=sx;sx=0;}
    if(sy<0){dy=-sy*k;sh+=sy;sy=0;}
    if(sx+sw>IW)sw=IW-sx;
    if(sy+sh>IH)sh=IH-sy;
    if(sw>0&&sh>0)g.drawImage(img,sx,sy,sw,sh,dx,dy,sw*k,sh*k);
  }
  // ---- 实体点位：真实坐标 → 雷达坐标 ----
  const mx=x=>C+(x-hx)*MM_K, my=y=>C+(y-hy)*MM_K;
  const R2=(R-3)*(R-3);
  const inside=(px,py)=>{const a=px-C,b=py-C;return a*a+b*b<=R2;};
  const dot=(x,y,color,r)=>{const px=mx(x),py=my(y);if(!inside(px,py))return;g.fillStyle=color;g.beginPath();g.arc(px,py,r||3,0,Math.PI*2);g.fill();};
  // 传送门（蓝）
  for(const gt of (worldMap.gates||[]))dot(gt.x,gt.y,'#6fc8ff',3.5);
  // 怪物（红）
  for(const m of worldMap.monsters)if(m.state==='idle')dot(m.x,m.y,'#ff5d5d',3);
  // 宝箱（金）：v1.9.46 只画未开启的——开过的箱子要从雷达上消失（opening/opened 都跳过）
  for(const c of worldMap.chests){ if(c.state!=='closed')continue; dot(c.x,c.y,'#f0d590',3); }
  // 其他玩家（青）
  for(const k2 in (worldMap.remote||{})){const r=worldMap.remote[k2];if(r)dot(r.x,r.y,'#8fd8ff',3.5);}
  // ---- 角色：连续角度指针（8 方向/斜向都能正确指示，不再退化成 3 个方向） ----
  // v1.9.6：旧版只有 4 个离散角度 + 三角形从圆心画起，被绿点盖住大半 —— 玩家实际
  // 只能看到右/左/下三个方向的尖角，上方向几乎完全被圆点遮住，看起来像"只有三个方向"。
  // 改为：以移动向量 atan2 得到的连续角度驱动，指针整体位于圆点之外并加深色描边。
  const hd=worldMap.hero;
  let ang=(typeof hd.ang==='number'&&isFinite(hd.ang))?hd.ang:null;
  if(ang===null){
    const d4=hd.facing||hd.dir||'down';
    ang={up:-Math.PI/2,down:Math.PI/2,left:Math.PI,right:0}[d4];
    if(ang==null)ang=Math.PI/2;
  }
  g.save();g.translate(C,C);g.rotate(ang);
  // 视野扇形（方向感的弱提示，不会盖住地形）
  const cone=g.createRadialGradient(0,0,4,0,0,26);
  cone.addColorStop(0,'rgba(150,255,196,.22)');
  cone.addColorStop(.55,'rgba(150,255,196,.08)');
  cone.addColorStop(1,'rgba(150,255,196,0)');
  g.fillStyle=cone;g.beginPath();g.moveTo(0,0);g.arc(0,0,26,-0.44,0.44);g.closePath();g.fill();
  // 指针：从圆点边缘起笔、末端带缺口，长短足够在浅色地形上分辨
  g.beginPath();
  g.moveTo(20,0);g.lineTo(7,-6.2);g.lineTo(9.6,0);g.lineTo(7,6.2);g.closePath();
  g.fillStyle='#eafff3';
  g.strokeStyle='rgba(8,24,15,.92)';g.lineWidth=1.6;g.lineJoin='round';
  g.fill();g.stroke();
  g.restore();
  // 圆心绿点（最后画，压在指针根部，形成"从圆点射出的指针"观感）
  g.fillStyle='#7fe0a0';g.beginPath();g.arc(C,C,4.6,0,Math.PI*2);g.fill();
  g.strokeStyle='#fff';g.lineWidth=1.5;g.stroke();
  g.restore();
  // ---- 外圈（金环 + 内细环），圆外为方向标 ----
  g.strokeStyle='rgba(217,180,91,.35)';g.lineWidth=1;
  g.beginPath();g.arc(C,C,R-4,0,Math.PI*2);g.stroke();
  g.strokeStyle='#d9b45b';g.lineWidth=3;
  g.beginPath();g.arc(C,C,R,0,Math.PI*2);g.stroke();
  // ---- v1.9.64 地图旗标：雷达范围内画小旗；范围外且开启跟踪时沿圆边画指向箭头 ----
  // v1.9.82 多个标记点：逐个画，跟踪中的金色、未跟踪的灰色，箭头按顺序错开半格避免完全重叠
  const wpins=(worldMap.worldPins?worldMap.worldPins():[]);
  wpins.forEach((pin,i)=>{
    const px=mx(pin.x), py=my(pin.y), ddx=px-C, ddy=py-C, dd=Math.hypot(ddx,ddy);
    const gold=pin.track;
    if(dd<=R-7){
      g.save();
      g.strokeStyle=gold?'#6b4c00':'#2a3442';g.lineWidth=1.8;
      g.beginPath();g.moveTo(px,py);g.lineTo(px,py-11);g.stroke();
      g.fillStyle=gold?'#ffd65a':'#8fa0b8';g.strokeStyle=gold?'#6b4c00':'#2a3442';g.lineWidth=1.1;
      g.beginPath();g.moveTo(px+1,py-11);g.lineTo(px+10,py-7.6);g.lineTo(px+1,py-4.2);g.closePath();
      g.fill();g.stroke();g.restore();
    } else if(gold){
      const a=Math.atan2(ddy,ddx), rr=R-9-i*0.9;
      const ex=C+Math.cos(a)*rr, ey=C+Math.sin(a)*rr;
      g.save();g.translate(ex,ey);g.rotate(a);
      g.fillStyle='#ffd65a';g.strokeStyle='rgba(26,16,0,.9)';g.lineWidth=1.3;
      g.beginPath();g.moveTo(9,0);g.lineTo(-4.5,-6);g.lineTo(-1.8,0);g.lineTo(-4.5,6);g.closePath();
      g.fill();g.stroke();g.restore();
    }
  });
  refreshMmPin();
}

let bAnim={hero:'idle',mon:'idle',heroUntil:0,monUntil:0};
let bHp={hero:640,heroMax:640,mon:420,monMax:420};
let bFx=[];
// v1.9.51 攻击动画特效：近战冲刺位移 + 远程法球投射（纯前端渲染层，不改服务端战报）
let heroLunge=null;    // {start,dur,tx,ty} 英雄冲刺到目标怪物身边的位移动画
let magicShots=[];     // 飞行中的法球 {x0,y0,x1,y1,start,dur,color,r}
let lastHeroX=420;     // 本帧英雄实际 X（供调试钩子实测）
let battleHeroActor=null, battleMonActors=[], battleMonState=[], battleMonInfos=[];
// v1.9.36 群怪：战斗座位（主怪居中，副怪左右错位稍后稍小，形成"敌方小队"纵深）
const MON_SLOT=[{x:858,y:558,s:3.2},{x:722,y:574,s:2.55},{x:992,y:576,s:2.55}];
function setBAnim(who,s,dur){
  bAnim[who]=s;bAnim[who+'Until']=performance.now()+(dur||0);
  bAnim[who+'Start']=performance.now();
  if(who==='hero'&&battleHeroActor)battleHeroActor.play(s);
  if(who==='mon')battleMonActors.forEach((a,i)=>setMonAnim(i,s,dur));
}
function setMonAnim(i,s,dur){
  if(!battleMonActors[i])return;
  battleMonState[i]={state:s,until:performance.now()+(dur||0),start:performance.now()};
  battleMonActors[i].play(s);
}
function curMonAnim(i){const st=battleMonState[i];return st&&performance.now()<st.until?st.state:'idle';}
function curBAnim(who){return performance.now()<(bAnim[who+'Until']||0)?bAnim[who]:'idle';}
function spawnFx(text,x,y,color){bFx.push({text:String(text),x,y,color:color||'#ff6b6b',born:performance.now()});}

/* v1.9.51 职业系攻击动画：战士系近战冲刺贴身，法师系远程法球投射（含转职） */
const MELEE_CLASSES={warrior:1,fighter:1,knight:1};   // 战士 / 拳师 / 骑士
const CASTER_CLASSES={mage:1,sage:1,warlock:1};       // 法师 / 祭祀 / 术士
function heroIsMelee(){const id=(state&&state.core&&state.core.classId)||'warrior';return MELEE_CLASSES[id]===1;}
function heroIsCaster(){const id=(state&&state.core&&state.core.classId)||'warrior';return CASTER_CLASSES[id]===1;}
// 近战冲刺：0→150ms 冲到怪物身侧（贴身），之后回位；150ms 正好是现有战报的受击时点
function startHeroLunge(sp){
  const tx=Math.max(520,((sp&&sp.x)||858)-160);
  heroLunge={start:performance.now(),dur:430,tx};
}
// 远程法球：从英雄手侧飞向怪物胸口，165ms 抵达（对齐受击时点），带拖尾 / 光晕 / 命中爆裂
function fireMagicShot(sp,fx){
  const color = fx==='fx_dmg_fire'?'#ff9b4a' : fx==='fx_dmg_holy'?'#ffe9a8'
              : fx==='fx_dmg_dark'?'#b47bff' : fx==='fx_heal'?'#7fe0a0' : '#7fd9ff';
  magicShots.push({x0:462,y0:470,x1:((sp&&sp.x)||858),y1:(((sp&&sp.y)||560)-120),
                   start:performance.now(),dur:165,color});
  if(magicShots.length>8) magicShots.shift();   // 防止极端连击堆积
}
function clearBattleFx(){heroLunge=null;magicShots=[];lastHeroX=420;}
// 实测钩子：暴露英雄实际 X 与法球轨迹，供 Playwright 逐帧采样
window.__fx=()=>({heroX:lastHeroX,melee:heroIsMelee(),
  lunge:heroLunge?{tx:heroLunge.tx,elapsed:performance.now()-heroLunge.start}:null,
  shots:magicShots.map(s=>({x0:s.x0,y0:s.y0,x1:s.x1,y1:s.y1,elapsed:performance.now()-s.start,color:s.color}))});
// 准备战斗部件 actor（英雄固定法师，怪物按战报阵容逐只建独立 actor，动画互不干扰）
function ensureBattleActors(monInfos){
  const hb=(state&&state.core)?heroBundleOf(state.core.classId):'char_warrior';
  if(!battleHeroActor||battleHeroActor.bundle!==hb){
    battleHeroActor=new window.StandeeActor(hb);
    battleHeroActor.init().then(()=>battleHeroActor.play('idle'));
  }
  // v1.9.36 群怪：按战报 monsters 建 N 只怪 actor；无战报信息时回退单怪（旧链路兼容）
  const list=(monInfos&&monInfos.length)?monInfos:[{sprite:(currentMonster&&(currentMonster.sprite||currentMonster.type))||'slime'}];
  const sig=list.map(m=>m.sprite||m.id||'slime').join(',');
  if(battleMonSig!==sig||!battleMonActors.length){
    battleMonSig=sig;
    battleMonInfos=list;
    battleMonActors=list.map(m=>{
      const sp=m.sprite||m.id||'slime';
      const bundle=window.MON_BUNDLE&&window.MON_BUNDLE[sp]?window.MON_BUNDLE[sp]:'mon_'+sp;
      const a=new window.PartActor(bundle);
      a.init().then(()=>a.play('idle'));
      return a;
    });
    battleMonState=list.map(()=>({state:'idle',until:0,start:0}));
  }
}
let battleMonSig='';
/* 战斗背景：程序化"天空光晕 + 三层山脊 + 草叶地面 + 前景剪影"，按世界主题换色。
   静态部分烘焙到离屏 canvas 缓存（每帧只做一次 drawImage），动态光尘另画。 */
const BG_PAL={
  grassland:{sky:['#4d7da6','#8fb8cc','#e2ecc6'],sun:'rgba(255,240,198,.5)',
    far:'#74976b',mid:'#53764f',near:'#3a5b3a',ground:'#7fa055',groundD:'#5d7d41',haze:'rgba(233,244,210,.30)'},
  forest:{sky:['#2f4a3f','#5c7f6a','#9dbb96'],sun:'rgba(206,240,180,.28)',
    far:'#3d5f3d',mid:'#2b472c',near:'#1c311c',ground:'#496639',groundD:'#35502c',haze:'rgba(176,212,182,.24)'},
  desert:{sky:['#b8773f','#dda86b','#f7e2b4'],sun:'rgba(255,226,150,.55)',
    far:'#b08a58',mid:'#96713f',near:'#775430',ground:'#c79660',groundD:'#a1764a',haze:'rgba(255,226,178,.30)'},
  snow:{sky:['#6d93b3','#a9c6da','#eaf4fb'],sun:'rgba(255,255,255,.5)',
    far:'#a3b9ca',mid:'#839db1',near:'#647d92',ground:'#c9dbe8',groundD:'#a5bdcd',haze:'rgba(240,250,255,.34)'},
  cave:{sky:['#332b1e','#5c4d36','#95805d'],sun:'rgba(255,205,130,.3)',
    far:'#4a3f2e',mid:'#382f22',near:'#251f15',ground:'#74644a',groundD:'#584a36',haze:'rgba(196,168,124,.22)'},
  arena:{sky:['#171325','#2f2746','#52476f'],sun:'rgba(180,160,255,.35)',
    far:'#312a47',mid:'#241f36',near:'#161227',ground:'#4b4560',groundD:'#37324a',haze:'rgba(150,130,200,.22)'}
};
const _bgCache={};
function buildBattleBg(g,key){
  const p=BG_PAL[key]||BG_PAL.grassland;
  const HZ=470; // 地平线
  // 1) 天空三段渐变
  const sky=g.createLinearGradient(0,0,0,HZ);
  sky.addColorStop(0,p.sky[0]);sky.addColorStop(.62,p.sky[1]);sky.addColorStop(1,p.sky[2]);
  g.fillStyle=sky;g.fillRect(0,0,1280,HZ);
  // 2) 日晕（左上斜射光）
  const sun=g.createRadialGradient(960,120,10,960,120,520);
  sun.addColorStop(0,p.sun);sun.addColorStop(.42,'rgba(255,246,214,.12)');sun.addColorStop(1,'rgba(255,246,214,0)');
  g.fillStyle=sun;g.fillRect(0,0,1280,HZ);
  // 3) 云带（三层，越远越淡；blur 让边缘化开，不像叠加的椭圆）
  const canBlur=typeof g.filter==='string';
  if(canBlur)g.filter='blur(9px)';
  for(let i=0;i<3;i++){
    const y=90+i*74, a=.10-i*.022, w=210+i*130, h=26+i*8;
    g.fillStyle=`rgba(255,255,255,${a})`;
    for(let k=0;k<4;k++){
      const cx=((i*317+k*263)%1420)-90;
      g.beginPath();g.ellipse(cx,y+k*5,w*(0.6+((k*37)%40)/100),h,0,0,7);g.fill();
    }
  }
  if(canBlur)g.filter='none';
  // 4) 三层山脊 + 谷雾
  const ridge=(yBase,amp,color,seed,step)=>{
    g.fillStyle=color;g.beginPath();g.moveTo(0,yBase+80);
    for(let x=0;x<=1280;x+=step){
      const h=Math.sin((x+seed)*0.0055)*amp+Math.sin((x+seed)*0.016)*amp*0.42+Math.sin((x+seed)*0.041)*amp*0.16;
      g.lineTo(x,yBase-h);
    }
    g.lineTo(1280,yBase+80);g.closePath();g.fill();
  };
  ridge(392,58,p.far,0,26);
  g.fillStyle=p.haze;g.fillRect(0,392,1280,34);
  ridge(430,40,p.mid,120,22);
  g.fillStyle=p.haze;g.fillRect(0,424,1280,26);
  ridge(462,24,p.near,260,18);
  // 5) 地面
  const gd=g.createLinearGradient(0,HZ-6,0,720);
  gd.addColorStop(0,p.groundD);gd.addColorStop(.22,p.ground);gd.addColorStop(1,p.groundD);
  g.fillStyle=gd;g.fillRect(0,HZ-6,1280,720-HZ+6);
  // 6) 地平线亮带（压住山与地的接缝）
  const hl=g.createLinearGradient(0,HZ-10,0,HZ+26);
  hl.addColorStop(0,'rgba(255,255,255,0)');hl.addColorStop(.5,'rgba(255,255,255,.16)');hl.addColorStop(1,'rgba(255,255,255,0)');
  g.fillStyle=hl;g.fillRect(0,HZ-10,1280,36);
  // 7) 地面横纹 + 草叶（近大远小，确定性随机）
  g.strokeStyle='rgba(0,0,0,.09)';g.lineWidth=2;
  for(let i=0;i<8;i++){
    const y=HZ+16+i*i*3.4+i*16;
    g.beginPath();g.moveTo(0,y);g.lineTo(1280,y+5);g.stroke();
  }
  for(let i=0;i<520;i++){
    const rnd=(n)=>{const v=Math.sin((i*12.9898+n*78.233))*43758.5453;return v-Math.floor(v);};
    const y=HZ+6+Math.pow(rnd(1),1.7)*250;              // 越近越密
    const x=rnd(2)*1280, sc=(y-HZ)/250;                  // 0 远 → 1 近
    const hgt=2+sc*9, tone=rnd(3);
    g.strokeStyle=tone>.72?'rgba(255,255,255,.16)':(tone>.4?'rgba(0,0,0,.16)':'rgba(0,0,0,.09)');
    g.lineWidth=sc>.6?1.6:1;
    g.beginPath();g.moveTo(x,y);g.lineTo(x+(rnd(4)-.5)*5*sc,y-hgt);g.stroke();
  }
  // 8) 中央战斗区光斑
  const spot=g.createRadialGradient(640,560,30,640,560,430);
  spot.addColorStop(0,'rgba(255,242,206,.13)');spot.addColorStop(1,'rgba(255,242,206,0)');
  g.fillStyle=spot;g.fillRect(0,430,1280,290);
  // 9) 前景草丛剪影
  g.fillStyle='rgba(10,20,12,.5)';
  g.beginPath();g.moveTo(0,720);
  for(let x=0;x<=1280;x+=10){
    const h=16+Math.sin(x*0.07)*6+Math.sin(x*0.23)*4;
    g.lineTo(x,720-h);
  }
  g.lineTo(1280,720);g.closePath();g.fill();
}
function drawBattleBg(key,t){
  let c=_bgCache[key];
  if(!c){
    c=document.createElement('canvas');c.width=1280;c.height=720;
    buildBattleBg(c.getContext('2d'),key);
    _bgCache[key]=c;
  }
  cvb.drawImage(c,0,0);
  // 动态：漂浮光尘（确定性循环，不占内存）
  for(let i=0;i<16;i++){
    const ph=(t*0.18+i*0.61)%1;
    const x=(i*173+ph*40)%1280, y=470-ph*300;
    const a=Math.sin(ph*Math.PI)*0.30;
    cvb.fillStyle=`rgba(255,246,206,${a})`;
    cvb.fillRect(x,y,2,2);
  }
}
function drawBattle(t,now){
  const key=(worldMap&&worldMap.themeKey)||'grassland';
  drawBattleBg(key,t);
  ensureBattleActors(battleMonInfos.length?battleMonInfos:null);
  // 死亡/战败是"非循环动作，播完保持最后一帧"：若这里按 curBAnim 到期就回 idle，
  // 英雄会在 bAnim 时长（600ms）结束后自己站起来 —— 玩家看到"倒地后又活了"。
  // 因此 die 状态必须由外部（下一场战斗开始 / 离开战斗）显式复位，这里不碰。
  if(curBAnim('hero')==='idle'&&bAnim.hero!=='die'&&battleHeroActor&&battleHeroActor.state!=='idle')battleHeroActor.play('idle');
  // v1.9.36 群怪：每只怪独立到期回 idle（die 由外部复位，同英雄规则）
  battleMonActors.forEach((a,i)=>{
    if(curMonAnim(i)==='idle'&&battleMonState[i]&&battleMonState[i].state!=='die'&&a.state!=='idle')a.play('idle');
  });
  // 站位：英雄 420；怪物按 MON_SLOT 座位（主怪 858，副怪 722/992 错位纵深）
  const heroFacesR=(state&&state.core)?artFacesRight(state.core.classId):true;
  // v1.9.51 近战冲刺位移：前 35% 冲向怪物（缓出），后 65% 退回（缓入），150ms 处恰在贴身顶点
  let heroX=420;
  if(heroLunge){
    const e=(now-heroLunge.start)/heroLunge.dur;
    if(e>=1){ heroLunge=null; }
    else{
      let q;
      if(e<0.35) q=1-Math.pow(1-e/0.35,3);          // 冲出去：easeOutCubic
      else { const u=(e-0.35)/0.65; q=1-u*u*(3-2*u); } // 退回来：smoothstep
      heroX=420+(heroLunge.tx-420)*q;
      // 冲刺残影：仅在去程绘制两道淡影，强化速度感（不叠在静止帧上）
      if(e<0.4&&battleHeroActor){
        for(let k=1;k<=2;k++){
          const back=heroX-k*26*(1-e/0.4);
          battleHeroActor.render(cvb,back,560,1.6,{dir:heroFacesR?1:-1,alpha:0.22-k*0.07});
        }
      }
    }
  }
  lastHeroX=heroX;
  // v1.9.71 组队副本：多英雄模式下由 drawPartyHeroes 画全队，不再画单人立绘
  if(!partySlots&&battleHeroActor)battleHeroActor.render(cvb,heroX,560,1.6,{dir:heroFacesR?1:-1});
  if(partySlots)drawPartyHeroes(t,now);
  // v1.9.27 怪物死亡：die 动画 500ms 内压扁 → 380ms 后收掉 actor 用塌陷尘团替代（群怪每只独立）
  const monDie=(bAnim.mon==='die')?(now-(bAnim.monStart||now)):-1;
  battleMonActors.forEach((a,i)=>{
    const slot=MON_SLOT[Math.min(i,MON_SLOT.length-1)];
    const isBossOnly=battleMonActors.length===1&&battleMonInfos[0]&&battleMonInfos[0].isBoss;
    const sc=isBossOnly?3.8:(battleMonActors.length>1?(i===0?2.8:slot.s):slot.s);
    const fade=monDie>=0?Math.max(0,1-monDie/380):1;
    // 阴影（淡出随死亡）
    cvb.fillStyle='rgba(0,0,0,'+(0.34*fade).toFixed(3)+')';
    cvb.beginPath();cvb.ellipse(slot.x,slot.y+8,58*(sc/3.2),15,0,0,7);cvb.fill();
    if(fade>0.02)a.render(cvb,slot.x,slot.y,sc,{dir:-1});
    if(monDie>=0&&fade<1){
      // 塌陷收尾：贴地尘团扩散 + 淡出
      const e=monDie/700;
      cvb.fillStyle='rgba(196,186,164,'+(0.26*(1-e)).toFixed(3)+')';
      for(let k=0;k<7;k++){
        const ang=k*0.9+e*1.2, r=(18+e*46)*(0.55+((k*37)%40)/60);
        cvb.beginPath();cvb.ellipse(slot.x+Math.cos(ang)*r*1.5,slot.y+8+Math.sin(ang)*r*0.28,r*0.62,r*0.26,0,0,7);cvb.fill();
      }
    }
  });
  // v1.9.51 法球投射：飞行段（拖尾+光晕+核心）→ 命中爆裂段（放射线+冲击环）
  if(magicShots.length){
    cvb.save();
    magicShots=magicShots.filter(s=>now-s.start<s.dur+420);
    for(const s of magicShots){
      const e=Math.min(1,(now-s.start)/s.dur);
      const sm=e*e*(3-2*e);
      const px=u=>s.x0+(s.x1-s.x0)*u;
      const py=u=>s.y0+(s.y1-s.y0)*u-Math.sin(Math.PI*u)*46;   // 抛物线弧
      if(e<1){
        for(let i=3;i>=1;i--){                                   // 拖尾
          const te=Math.max(0,e-i*0.07), tes=te*te*(3-2*te);
          cvb.globalAlpha=0.46-i*0.12;cvb.fillStyle=s.color;
          cvb.beginPath();cvb.arc(px(tes),py(tes),9-i*2,0,7);cvb.fill();
        }
        const x=px(sm),y=py(sm);
        cvb.globalAlpha=1;
        const gr=cvb.createRadialGradient(x,y,1,x,y,24);
        gr.addColorStop(0,'#ffffff');gr.addColorStop(0.38,s.color);gr.addColorStop(1,'rgba(0,0,0,0)');
        cvb.fillStyle=gr;cvb.beginPath();cvb.arc(x,y,24,0,7);cvb.fill();
        cvb.fillStyle='#fff';cvb.beginPath();cvb.arc(x,y,5.5,0,7);cvb.fill();
      }else{                                                    // 命中爆裂
        const bt=(now-s.start-s.dur)/420;
        if(bt<1){
          cvb.globalAlpha=1-bt;cvb.strokeStyle=s.color;cvb.lineWidth=3;
          for(let k=0;k<8;k++){
            const a=k*Math.PI/4, r=12+bt*54;
            cvb.beginPath();
            cvb.moveTo(s.x1+Math.cos(a)*r*0.65,s.y1+Math.sin(a)*r*0.42);
            cvb.lineTo(s.x1+Math.cos(a)*r,s.y1+Math.sin(a)*r*0.65);
            cvb.stroke();
          }
          cvb.lineWidth=2;cvb.globalAlpha=(1-bt)*0.6;
          cvb.beginPath();cvb.ellipse(s.x1,s.y1,10+bt*54,(10+bt*54)*0.6,0,0,7);cvb.stroke();
        }
      }
    }
    cvb.restore();
  }
  bFx=bFx.filter(f=>now-f.born<620);
  for(const f of bFx){
    const age=(now-f.born)/620;
    cvb.save();cvb.globalAlpha=1-age;
    cvb.font='bold 30px sans-serif';cvb.textAlign='center';
    cvb.strokeStyle='#000';cvb.lineWidth=5;cvb.strokeText(f.text,f.x,f.y-50*age);
    cvb.fillStyle=f.color||'#ff6b6b';cvb.fillText(f.text,f.x,f.y-50*age);
    cvb.restore();
  }
}
function renderLoop(now){
  const t=now/1000;
  perfTick(now);
  if(view==='world'){
    // 面板/弹窗打开时大世界被完全遮住，继续每帧重绘纯属浪费（实测面板页会掉到 41 FPS）
    if(!panel && document.getElementById('modalLayer').classList.contains('hidden')) worldMap.render(now);
    // 小地图/交互提示降频 10Hz：避免每帧重绘小画布 + DOM 写入造成卡顿
    if(now-(renderLoop._auxTs||0)>100){
      renderLoop._auxTs=now;
      drawMiniMap();
      updateInteractHint();
    }
  }
  else if(view==='battle') drawBattle(t,now);
  requestAnimationFrame(renderLoop);
}
/* 性能角标：版本 + FPS + 本机 GPU 纹理上限（判断超大地图是否触碰硬件上限） */
const BUILD_VER='v1.9.83';
let _perfFrames=0,_perfTs=0,_perfFps=0,_texMax='?';
function perfTick(now){
  _perfFrames++;
  if(now-_perfTs>=1000){
    _perfFps=Math.round(_perfFrames*1000/(now-_perfTs));
    _perfFrames=0;_perfTs=now;
    if(_texMax==='?'){
      try{
        const gl=document.createElement('canvas').getContext('webgl');
        _texMax=gl?String(gl.getParameter(gl.MAX_TEXTURE_SIZE)):'无WebGL';
      }catch(e){_texMax='无WebGL';}
    }
    // v1.9.80 性能角标仅调试用：地址带 ?perf=1 才显示，玩家默认看不到
    const el=$('perfBadge');
    if(el){
      if(/[?&]perf=1/.test(location.search)){
        el.style.display='block';
        el.textContent=`${BUILD_VER} · ${_perfFps} FPS`;
        el.title=`纹理上限 ${_texMax}`;
      }
    }
  }
}
// 靠近实体时的交互提示（跟随角色头顶，10Hz 更新；仅在内容变化时重写 innerHTML）
let _hintKey='';
function updateInteractHint(){
  const h=worldMap.hero, el=$('interactHint');
  if(!h||!worldMap.ready){el.classList.add('hidden');_hintKey='';return;}
  const mon=worldMap.nearestMonster(h.x,h.y,90);
  const ch=worldMap.nearestChest(h.x,h.y,80);
  let key='',html='';
  if(mon){key='m'+mon.nodeId;html=ICON.sword(15,'#f0d590')+'<span>点击「'+mon.name+'」交战</span>';}
  else if(ch){key='c'+ch.nodeId;html=ICON.chest(15,'#f0d590')+'<span>点击宝箱开启（'+({wood:'木',silver:'银',gold:'金',diamond:'钻石',crystal:'水晶'}[ch.tier])+'）</span>';}
  if(!key){el.classList.add('hidden');_hintKey='';return;}
  if(key!==_hintKey){_hintKey=key;el.innerHTML=html;}
  el.classList.remove('hidden');
  // 锚在角色头顶 86px：视线不用来回跳
  el.style.left=Math.round(h.x-worldMap.cam.x)+'px';
  el.style.top=Math.round(h.y-worldMap.cam.y-86)+'px';
}

/* ============================================================
   ★ 单通道导航状态机（核心）
   ============================================================ */
function hideModal(){ $('modalLayer').classList.add('hidden'); }
function showModal(html){ $('modalBox').innerHTML=html; $('modalLayer').classList.remove('hidden'); }

// 切换到顶层视图：离开 world 时强制收起所有面板与遇怪弹窗
function showView(v){
  view=v;
  const inWorld=(v==='world');
  const lo=$('logoutBtn'), sb=$('sfxBtn');
  if(lo){ lo.classList.toggle('hidden', v==='login'); lo.style.opacity=(v==='battle')?'0.35':''; }
  if(sb)sb.classList.toggle('hidden', v!=='world');
  $('loginView').classList.toggle('hidden', v!=='login');
  $('worldView').classList.toggle('hidden', v!=='world');
  $('battleView').classList.toggle('hidden', v!=='battle');
  const cv=$('createView'); if(cv)cv.classList.toggle('hidden', v!=='create');
  if(v!=='world'){
    panel=null; equipDetailSlot=null;
    $('panelLayer').classList.add('hidden');
    $('interactHint').classList.add('hidden');
    hideModal();
  }
  // 地图激活：仅大世界态（面板打开时通过 active 再关闭，防操作冲突）
  worldMap.active=(v==='world'&&!panel);
  // 退出登录按钮：登录后始终可见（唯一出口）
  $('logoutBtn').classList.toggle('hidden', !loggedUser);
}

/* ---- 登录页 ---- */
function refreshLoginView(){
  const ok=!!loggedUser;
  $('authBox').classList.toggle('hidden', ok);
  $('enterBox').classList.toggle('hidden', !ok);
  $('welcomeName').textContent=loggedUser||'';
  $('authErr').textContent='';
  $('logoutBtn').classList.toggle('hidden', !ok);
}
async function doRegister(){
  const u=$('authUser').value.trim(), p=$('authPwd').value;
  if(!u||!p){ $('authErr').textContent='请填写账号和密码'; return; }
  try{
    const r=await api('/api/auth/register',{username:u,password:p,deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username);
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
async function doLogin(){
  const u=$('authUser').value.trim(), p=$('authPwd').value;
  if(!u||!p){ $('authErr').textContent='请填写账号和密码'; return; }
  try{
    const r=await api('/api/auth/login',{username:u,password:p,deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username);
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
async function doDeviceLogin(){
  try{
    const r=await api('/api/auth/device',{deviceId:getDeviceId()});
    setSession(r.token,r.userId,r.username||('冒险者'+r.userId.slice(-4)));
    refreshLoginView();
  }catch(e){ $('authErr').textContent=e.message; }
}
function switchAccount(){ setSession(null); $('authUser').value=''; $('authPwd').value=''; state=null; closeWS(); refreshLoginView(); }

// 职业 → 整体立绘 bundle（探索/战斗同一套职业装备装，交付包 §4.2）
// v1.9.65 战士/骑士立绘素材互换了：parts.json 里 char_warrior 写的是"钢甲红披"、
// char_knight 写的是"银白重甲蓝羽"，但两个文件夹里的图正好相反（warrior 里是灰钢甲、
// knight 里是红甲）。游戏里表现为"战士显示成骑士、骑士显示成战士"，这里把映射对调。
const CLASS_BUNDLES={warrior:'char_knight',mage:'char_mage',knight:'char_warrior',fighter:'char_fighter',sage:'char_sage',warlock:'char_warlock'};
window.CLASS_BUNDLES=CLASS_BUNDLES;   // 供 creator.js / worldmap.js 共用同一张表
// v1.9.27 立绘美术默认朝向（对照 standee 素材逐个确认）：战士/拳师/术士面向右，
// 法师/骑士/祭祀面向左。渲染时按"移动方向 ⊕ 美术朝向"决定翻转，行走不再反向。
// v1.9.57 更正：v1.9.49 重绘后的六个职业立绘**全部朝右**（法杖/武器在后方手、光球/盾牌在朝前手），
// 原表把 mage/knight/sage 标成朝左——这三个职业走路反向（法师首当其冲）。
// 未知职业同样按朝右处理；将来某职业改朝左，在此单独标 false。
const ART_FACES_RIGHT={warrior:false,mage:true,knight:false,fighter:true,sage:true,warlock:true};
function artFacesRight(classId){ return ART_FACES_RIGHT[classId]!==false; }
function heroBundleOf(classId){ return CLASS_BUNDLES[classId]||'char_warrior'; }
function applyHeroLook(){
  if(!state||!state.core)return;
  const b=heroBundleOf(state.core.classId);
  if(worldMap.setHeroBundle)worldMap.setHeroBundle(b);
  if(worldMap.setHeroFacing)worldMap.setHeroFacing(artFacesRight(state.core.classId)); // v1.9.27 朝向修正
}
// 唯一入口：登录页 →（首次创角）→ 大世界
// v1.9.18 修复"点一次进不去"：点击立即给加载反馈并禁用按钮（公网隧道下首进需下载地图，可达数十秒，
// 之前无任何提示像卡死）；任何失败路径都会恢复按钮并显示原因。
function enterLoading(txt){ $('loadTxt').textContent=txt; $('loading').classList.remove('hidden'); const b=$('enterBtn'); if(b)b.disabled=true; }
function enterLoadingDone(err){
  const b=$('enterBtn'); if(b)b.disabled=false;
  if(err){ $('loadTxt').textContent='进入失败：'+err; setTimeout(()=>$('loading').classList.add('hidden'),2500); }
  else $('loading').classList.add('hidden');
}
async function enterGame(){
  if(!loggedUser){ showView('login'); return; }
  enterLoading('正在进入…');
  try{
    $('loadTxt').textContent='正在读取存档…';
    const r=await api('/api/state/load',{userId:uid()});
    if(r.exists){ state=r.state; if(r.stats) pendingStats=r.stats; }
    else { $('loadTxt').textContent='正在创建角色档案…'; state=await api('/api/character/new',{userId:uid(),name:loggedUser,classId:'warrior'}); }
    // v1.9.18：appearance.created=false → 强制进创建界面（交付包 §4.1，重连不会重复创建）
    if(!state.appearance||!state.appearance.created){
      enterLoadingDone();
      showView('create');
      window.Creator&&Creator.open({
        mode:'create', cfg:CFG, api:api, uid:uid(), sfx:window.SFX, toast:toastModal,
        name:(state.core&&state.core.name)||loggedUser,
        onDone:(st)=>{ state=st; finishEnter(); },
        onCancel:()=>{ showView('login'); refreshLoginView(); }
      });
      return;
    }
    await finishEnter();
  }catch(e){ enterLoadingDone(e.message); }
}
// 进入大世界（创角完成后也走这里，避免两套入口）
async function finishEnter(){
  try{
    $('loadTxt').textContent='正在计算角色属性…';
    try{
      // v1.9.28：发送全量装备（带 equipped 标记，含仓库装备）——
      // 服务端只把已穿戴的计入总属性（修复 v1.9.27 记录的虚增血量问题），同时逐件返回战力
      if(pendingStats&&pendingStats.atk!=null){ heroStats=pendingStats; pendingStats=null; }  // v1.9.60 省一次往返
      else{ const whEqAll=(state.warehouse||[]).filter(it=>it.kind==='equip'&&it.equip).map(it=>Object.assign({},it.equip,{equipped:false}));
        heroStats=await api('/api/stats/calc',{level:state.core.level,classId:state.core.classId,equip:(state.equip||[]).concat(whEqAll)}); }
    }catch(e){ heroStats=null; }
    syncHud();
    applyHeroLook();
    // 初始化地图引擎（首次）
    if(!worldMap.ready){
      $('loadTxt').textContent='正在生成大世界…（首次进入需下载地图，请稍候）';
      worldMap.opts.worldDefs=(CFG&&CFG.worlds&&CFG.worlds.worlds)||[];
      await worldMap.load();
    }
    applyHeroLook();
    // 进入玩家当前世界并拉取服务端点位
    $('loadTxt').textContent='正在连接世界点位…';
    await loadWorld(state.world.worldId||'W1', state.world.x, state.world.y);
    showView('world');
    enterLoadingDone();
    $('sfxBtn')&&$('sfxBtn').classList.remove('hidden');
    window.SFX&&SFX.login();
    connectWS();
    startInviteWatch();   // v1.9.68 组队邀请轮询（登录后启动，未组队时才真正发请求）
    // v1.9.45 任务栏收起/展开：Q 键与右上角箭头共用同一状态
    window.toggleQuestTracker=(force)=>{
      const box=document.getElementById('questTracker');
      const btn=document.getElementById('qtToggle');
      if(!box)return;
      const collapsed=(typeof force==='boolean')?box.classList.toggle('collapsed',force):box.classList.toggle('collapsed');
      if(btn)btn.setAttribute('aria-expanded',String(!collapsed));
      window.SFX&&SFX.tab&&SFX.tab();
    };
    const _qtBtn=document.getElementById('qtToggle');
    if(_qtBtn)_qtBtn.onclick=(e)=>{ e.stopPropagation(); window.toggleQuestTracker(); };
    // v1.9.29 大地图快捷键：M 开/关，Esc 关，Q 收/展任务栏（输入框聚焦与游戏弹窗打开时不触发）
    addEventListener('keydown',e=>{
      if(!worldMap)return;
      const tag=(e.target&&e.target.tagName)||'';
      if(tag==='INPUT'||tag==='TEXTAREA'||e.target.isContentEditable)return;
      const k=e.key.toLowerCase();
      const gameModal=!document.getElementById('modalLayer').classList.contains('hidden');
      // v1.9.50 严格检测：仓库批量选择模式按 Esc 退出（不超过弹窗）
      if(!gameModal&&k==='escape'&&whBatch){ e.preventDefault(); clearWhBatch(); if(typeof renderPanel==='function')renderPanel(); return; }
      // v1.9.47 弹窗（结算 / 在线玩家 / 玩家档案）现在浮在大地图之上（z-index 130 > 120），
      // 弹窗开着时按 M / Esc 不能再关掉被它盖住的地图，否则关掉弹窗会发现地图莫名消失。
      if(gameModal)return;
      if(k==='m'&&worldMap.mapModalOpen){ e.preventDefault(); worldMap.closeMapModal(); }
      else if(k==='m'&&worldMap.active&&!gameModal){ e.preventDefault(); worldMap.openMapModal(); }
      else if(k==='escape'&&worldMap.mapModalOpen){ worldMap.closeMapModal(); }
      else if(k==='q'&&worldMap.active&&!gameModal&&!document.body.classList.contains('panel-open')){ e.preventDefault(); window.toggleQuestTracker(); }
    });
    // 主线任务：绑定当前账号，注入服务端领取与状态回调
    if(window.Quest) Quest.init(uid(),{
      claim:(qid)=>api('/api/player/action',{userId:uid(),action:'quest_claim',questId:qid}),
      getState:()=>state,
      onState:(st)=>{ state=st; refreshStats().then(syncHud); },
      toast:(t,s)=>{window.SFX&&SFX.quest();toastModal(t,s);}
    });
    // v1.9.27 回血药水快用按钮（HUD）；v1.9.41 满血本地预检（不发请求）+ 去掉多余 refreshStats（降交互延迟）
    const pb=$('usePotionBtn');
    const hpFull=()=>{ const maxHp=(heroStats&&heroStats.hp)||0; const hp=(state&&state.core&&typeof state.core.hp==='number'&&state.core.hp>0)?state.core.hp:maxHp; return maxHp>0&&hp>=maxHp; };
    if(pb)pb.onclick=async()=>{
      if(hpFull()){ window.SFX&&SFX.fail(); toastModal('血量已满','当前血量已满，无需使用药水'); return; }
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'use_potion',itemId:'potion_s'});
        if(r.state){state=r.state;syncHud();}
        window.SFX&&SFX.heal();
        toastModal('已使用回血药水','血量回复 +'+Math.round(r.healed||0)+'（当前 '+Math.round(r.hp||0)+'/'+Math.round(r.maxHp||0)+'）');
      }catch(e){window.SFX&&SFX.fail();toastModal('无法使用',e.message);}
    };
  }catch(e){ enterLoadingDone(e.message); }
}
// 加载世界：切主题地图 + 拉取 ACTIVE 点位
async function loadWorld(worldId, heroX, heroY){
  const spots=await api('/api/spawn?worldId='+encodeURIComponent(worldId)+'&userId='+encodeURIComponent(uid()));
  await worldMap.switchWorld(worldId, spots.spots, heroX, heroY);
  const wd=(CFG.worlds.worlds||[]).find(w=>w.id===worldId);
  // v1.9.46 顶部世界横幅与左侧场景名条已移除：世界名只在小地图下方显示一处，
  // 同时写进 window.__worldLabel 供传送提示 / 大地图弹窗标题复用（原来读 #worldTitle）。
  const worldLabel=(worldId==='HOME')?((wd&&wd.name)||'我的家园'):(worldId+' · '+(wd?wd.name:'大世界'));
  window.__worldLabel=worldLabel;
  const mmw=$('mmWorld');
  if(mmw) mmw.textContent=worldLabel;
  syncHud();
}
// v1.9.31 家园功能点：仓库/挂机修炼/换装镜（复用原家园面板，二级页直达）
function openHomeFunc(h){
  if(view!=='world'||panel||battlePlaying)return;
  if(!$('modalLayer').classList.contains('hidden'))return;
  window.SFX&&SFX.tab();
  if(h.kind==='warehouse'){ openPanel('home'); homeSub='warehouse'; renderPanel(); }
  else if(h.kind==='idle'){ openPanel('home'); homeSub='idle'; renderPanel(); }
  else { openPanel('home'); } // dress：家园面板主视图（换装 + 装饰）
}
// v1.9.31 底部「家园」按钮：进入家园地图（已在家园则打开家园面板）
async function enterHome(){
  if(view!=='world'||panel||battlePlaying)return;
  if(!$('modalLayer').classList.contains('hidden'))return;
  if(worldMap.worldId==='HOME'){ openPanel('home'); return; }
  worldMap.active=false;
  $('loadTxt').textContent='正在回到家园…';
  $('loading').classList.remove('hidden');
  try{
    const r=await api('/api/world/travel',{to:'HOME',userId:uid()});
    if(r.state){state=r.state;syncHud();}
    $('modalLayer').classList.add('hidden');
    $('loadTxt').textContent='正在进入家园…';
    await loadWorld('HOME', state.world.x, state.world.y);
    window.SFX&&SFX.gate();
    setTimeout(()=>{window.SFX&&SFX.arrive();},420);
    $('loading').classList.add('hidden');
    toastModal('已回到家园','点击家园内的设施使用对应功能 · 走到发光大门可返回大世界');
    worldMap.active=true;
    if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'match_join',worldId:'HOME',name:loggedUser,classId:(state&&state.core&&state.core.classId)||''}));
  }catch(e){
    $('loading').classList.add('hidden');
    window.SFX&&SFX.fail();toastModal('进入家园失败',e.message);
    worldMap.active=true;
  }
}
// 点传送门 → 确认弹窗（单通道）
function askTravel(gate){
  if(battlePlaying||panel||view!=='world')return;
  worldMap.active=false;
  const req=gate.requireLevel||0;
  const lv=state?state.core.level:1;
  const locked=req>0 && lv<req;
  showModal(`<h2>世界通道</h2>
    <div class="msub">${gate.label}<br>当前等级 Lv.${lv} ${req>0?'· 需 Lv.'+req:''}${locked?'<br><span style="color:#ffcf7a">等级不足，请先打怪或挂机升至 Lv.'+req+'</span>':''}</div>
    <div class="divider"></div>
    <div class="enc-btns"><button class="btn-red" id="goTravel" ${locked?'disabled style="opacity:.45;cursor:not-allowed"':''}>${locked?'等级不足':'传送'}</button><button class="btn-gray" id="cancelTravel">留在原地</button></div>`);
  if(locked){ $('goTravel').onclick=null; }
  else { $('goTravel').onclick=()=>{hideModal();travelTo(gate);}; }
  $('cancelTravel').onclick=()=>{hideModal();worldMap.active=true;};
}
// 同端口 WebSocket：多人位置同步（断线退避重连 1/2/4/8s + 服务端快照恢复）
function connectWS(){
  closeWS(true); // 主动清理旧定时器，但不重置退避计数
  const proto=location.protocol==='https:'?'wss:':'ws:';
  clientId=authUserId||('c_'+Date.now());
  const url=`${proto}//${location.host}/ws?token=${encodeURIComponent(authToken||'')}&clientId=${encodeURIComponent(clientId)}`;
  try{
    ws=new WebSocket(url);
    ws.onopen=()=>{
      wsRetry=0; // 连接成功重置退避
      // 带旧 channel 重连，服务端未满则复用原线
      ws.send(JSON.stringify({type:'match_join',worldId:worldMap.worldId,channel:wsChannel,name:loggedUser,classId:(state&&state.core&&state.core.classId)||''}));
      $('wsHint').textContent='多人已连接'+(wsChannel>0?'·'+wsChannel+'线':'');
      wsTimers.push(setInterval(()=>{if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'heartbeat'}));},5000));
      // 定期上报位置（10Hz）
      wsTimers.push(setInterval(()=>{
        if(ws&&ws.readyState===1){
          const h=worldMap.hero;
          ws.send(JSON.stringify({type:'player_move',x:Math.round(h.x),y:Math.round(h.y),seq:Date.now()}));
        }
      },100));
    };
    ws.onmessage=e=>{
      let d;try{d=JSON.parse(e.data);}catch(err){return;}
      if(d.type==='match_joined'){
        wsChannel=d.channel||0;
        $('wsHint').textContent='多人已连接'+(wsChannel>0?'·'+wsChannel+'线':'');
      }else if(d.type==='state_snapshot'){
        worldMap.applySnapshot(d.players||[],clientId); // 视野裁剪在 worldmap 内做
        // v1.9.63 徽标只统计"本图其他玩家"：原来把自己也算进去，徽标与在线列表永远差 1
        onlineCount=(d.players||[]).filter(p=>p&&p.id!==clientId).length;
        $('onlineBadge').textContent='● 在线 '+onlineCount;
      }
    };
    ws.onclose=()=>{
      $('wsHint').textContent='连接断开·重连中';
      scheduleReconnect();
    };
    ws.onerror=()=>{ $('wsHint').textContent='连接异常·重连中'; };
  }catch(e){ $('wsHint').textContent='单人模式'; scheduleReconnect(); }
}
// 退避重连：1s→2s→4s→8s，上限 8s
function scheduleReconnect(){
  if(!authToken && !authUserId) return; // 未登录不重连
  const backoff=[1000,2000,4000,8000];
  const delay=backoff[Math.min(wsRetry,backoff.length-1)];
  wsRetry++;
  clearTimeout(wsReconnectTimer);
  wsReconnectTimer=setTimeout(()=>{ if(worldViewActive()) connectWS(); },delay);
}
let wsReconnectTimer=null;
function worldViewActive(){ const v=document.querySelector('.view:not(.hidden)'); return !v||v.id==='worldView'; }
// closeWS(keepRetry)：退出登录/切账号时彻底停止；重连建连时只清定时器保留退避
function closeWS(keepRetry){
  wsTimers.forEach(t=>clearInterval(t)); wsTimers=[];
  if(ws){try{ws.onclose=null;ws.close();}catch(e){}ws=null;}
  if(!keepRetry){ wsRetry=0; clearTimeout(wsReconnectTimer); wsChannel=0; }
}

// 切换世界（走 GATE，服务端校验门槛 + 落库）
// v1.9.24：传送全程加载层提示（公网首传需下载目标世界地图，无提示像卡死）；传送 CD 已取消
async function travelTo(gate){
  if(battlePlaying||panel)return;
  // v1.9.31 家园大门：to='BACK' → 返回进入家园前的世界（服务端存档记录）
  if(gate.to==='BACK'){
    gate=Object.assign({},gate,{to:(state&&state.world&&state.world.lastWorldId)||'W1'});
  }
  worldMap.active=false;                       // 传送期间锁定操作，防移动/重复交互
  $('loadTxt').textContent='正在穿越世界通道…';
  $('loading').classList.remove('hidden');
  try{
    const r=await api('/api/world/travel',{to:gate.to,userId:uid()});
    if(r.state){ state=r.state; }
    $('modalLayer').classList.add('hidden');
    $('loadTxt').textContent='正在加载目的地地图…（首次传送需下载地图，请稍候）';
    await loadWorld(gate.to, state.world.x, state.world.y);
    if(window.Quest&&gate.to==='W2')Quest.set('travel',1);
    window.SFX&&SFX.gate();
    setTimeout(()=>{window.SFX&&SFX.arrive();},420);
    $('loading').classList.add('hidden');
    toastModal('传送成功','已抵达 '+(window.__worldLabel||gate.to));
    worldMap.active=true;
    if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'match_join',worldId:gate.to,name:loggedUser,classId:(state&&state.core&&state.core.classId)||''}));
  }catch(e){
    $('loading').classList.add('hidden');
    window.SFX&&SFX.fail(); toastModal('传送失败',e.message);
    worldMap.active=true;
  }
}
// 唯一出口：任意处 → 登录页
function doLogout(){
  window.SFX&&SFX.panelClose();
  setSession(null); state=null; panel=null; equipDetailSlot=null;
  if(window.Quest&&$('questTracker'))$('questTracker').innerHTML='';
  closeWS(); worldMap.active=false;
  $('sfxBtn')&&$('sfxBtn').classList.add('hidden');
  showView('login'); refreshLoginView();
}

// 打开面板：只能从 world，且当前无任何面板/弹窗/遇怪
function openPanel(key){
  if(view!=='world') return;                                   // 战斗/登录态禁止
  if(panel) return;                                             // 禁止面板互串
  if(!$('modalLayer').classList.contains('hidden')) return;     // 弹窗中禁止
  if(battlePlaying) return;
  // v1.9.34 仓库只能在家园打开（背包随时可开，存取仅家园）
  if(key==='warehouse' && (!worldMap || worldMap.worldId!=='HOME')){
    window.SFX&&SFX.deny(); toastModal('仓库在家园','回家园后打开家园面板 › 仓库 即可管理仓库'); return;
  }
  panel=key; equipDetailSlot=null; forgeSub=false; forgeSel=null; homeSub=null;
  clearWhBatch();  // v1.9.50 严格检测：切换/关闭任何面板即退出仓库批量模式
  worldMap.active=false;                                        // 面板期间冻结地图
  const meta={role:'角色',home:'家园',dungeon:'副本',guild:'公会',map:'世界地图',warehouse:'仓库',idle:'挂机设置',bag:'背包',shop:'商店'};
  $('panelTitle').textContent=meta[key]||'';
  $('panelSub').textContent='';
  renderPanel();
  $('panelLayer').classList.remove('hidden');
  $('worldView').classList.add('panel-open');   // HUD 退到背景，避免"没盖住"的观感
  document.body.classList.add('panel-open');    // 全局出口按钮（退出登录）也要让位
  window.SFX&&SFX.panelOpen();
}
// 面板唯一出口：返回大世界
function closePanel(){
  if(view!=='world') return;
  panel=null; equipDetailSlot=null; forgeSub=false; forgeSel=null; homeSub=null;
  clearWhBatch();  // v1.9.50 严格检测：离开仓库界面即取消批量模式
  $('panelLayer').classList.add('hidden');
  $('worldView').classList.remove('panel-open');
  document.body.classList.remove('panel-open');
  worldMap.active=true;                                         // 恢复地图
  window.SFX&&SFX.panelClose();
}

/* ---- HUD 同步（大世界左上信息） ---- */
function expNeeded(level){
  const g=(CFG&&CFG.classes&&CFG.classes.levelGrowth)||{expBase:60,expGrowth:1.18};
  return Math.round((g.expBase||60)*Math.pow(g.expGrowth||1.18,Math.max(0,level-1)));
}
function fmt(n){return Number(n||0).toLocaleString('zh-CN');}
let _lastLv=0;
function syncHud(){
  if(!state||!state.core)return;
  window.__st=state;   // 调试钩子：自动化测试读取金币/家园等级（不影响玩法）
  const c=state.core;
  if(_lastLv&&c.level>_lastLv&&window.SFX)SFX.level();
  _lastLv=c.level;
  const cdef=CFG&&CFG.classes.classes.find(x=>x.id===c.classId);
  const cname=cdef?cdef.name:c.classId;
  $('worldName').textContent=cname+' · '+(c.name||'英雄');
  const power=heroStats&&heroStats.power?heroStats.power:c.battlePower;
  const need=expNeeded(c.level), exp=Math.max(0,c.exp||0), pct=Math.min(100,need>0?exp/need*100:0);
  const ef=$('expFill'), et=$('expTxt');
  if(ef){ ef.style.width=pct.toFixed(1)+'%'; }
  if(et){ et.textContent=`EXP ${fmt(exp)} / ${fmt(need)}`; }
  $('worldLv').innerHTML=`Lv.${c.level} · 战力 ${fmt(power)}`;
  // v1.9.53 金币/钻石/强化材料从角色条移到右上角独立显示（需求：不要和左上角角色放一起）
  const hc=$('hudCurrency');
  if(hc) hc.innerHTML=
    `<b class="hc hc-gold" title="金币">${ICON.gold(15)}<em>金币</em><i>${fmt(c.gold)}</i></b>`+
    `<b class="hc hc-gem" title="钻石">${ICON.gem(15)}<em>钻石</em><i>${fmt(c.gem)}</i></b>`+
    `<b class="hc hc-mat" title="强化材料">${ICON.crystal(15,'#9ad6ff')}<em>强化材料</em><i>${fmt(c.material)}</i></b>`;
  const iconKey=(cdef&&cdef.icon)?cdef.icon.replace('ui_card_','').replace('.png',''):'warrior';
  $('worldClsIco').src=RES+'characters/cards/ui_card_'+iconKey+'.png';
  $('heroName').textContent=(c.name||'英雄')+' Lv.'+c.level;
  // v1.9.27 全局血条：当前血量/最大血量（战斗后保留余量，不自动回满）
  const maxHp=(heroStats&&heroStats.hp)||0;
  const curHp=(typeof c.hp==='number'&&c.hp>0)?c.hp:maxHp;
  const hf=$('hpFill'), ht=$('hpTxt');
  if(hf){ hf.style.width=(maxHp>0?Math.max(0,Math.min(100,curHp/maxHp*100)):100).toFixed(1)+'%'; }
  if(ht){ ht.textContent=maxHp>0?`${Math.ceil(curHp)} / ${Math.ceil(maxHp)}`:'-- / --'; }
  const pc=$('potionCnt');
  if(pc){ pc.textContent=(state.warehouse||[]).filter(it=>it.kind==='consume'&&it.id==='potion_s').reduce((s,it)=>s+it.qty,0); }
  // v1.9.41 满血检测修复：血条满时 HUD 快用按钮置灰，视觉上直接明确"不需要喝"
  const pb=$('usePotionBtn');
  if(pb){ const full=maxHp>0&&curHp>=maxHp; pb.classList.toggle('hp-full',full); pb.title=full?'血量已满':'使用回血药水'; }
}
// v1.9.60 交互延迟优化：服务端已把属性随操作响应一起返回（r.stats），
// 直接采用即可，不再紧跟一次 /api/stats/calc——公网下单次往返 0.6~1.4 秒。
async function applyStats(s){
  if(s && s.atk!=null){ heroStats=s; syncHud(); return; }
  await refreshStats(); syncHud();
}
async function refreshStats(){
  if(!state||!state.core)return;
  try{
    // v1.9.28：发送全量装备（背包带 equipped 标记 + 仓库装备标记未穿戴）——
    // 服务端只把已穿戴的计入总属性，同时逐件返回战力贡献（equipPowers）供列表显示
    const whEq=(state.warehouse||[]).filter(it=>it.kind==='equip'&&it.equip).map(it=>Object.assign({},it.equip,{equipped:false}));
    heroStats=await api('/api/stats/calc',{level:state.core.level,classId:state.core.classId,equip:(state.equip||[]).concat(whEq)});
  }catch(e){}
}
function attrRows(){
  const st=heroStats||{};
  const rows=[
    ['攻击', Number(st.atk||0).toFixed(1), 'atk'],
    ['防御', Number(st.def||0).toFixed(1), 'def'],
    ['生命', Math.round(st.hp||0), 'hp'],
    ['暴击', (Number(st.crit||0)*100).toFixed(0)+'%', 'crit'],
    ['速度', Math.round(st.spd||10), 'spd'],
    ['装备', ((state&&state.equip)?state.equip.filter(e=>e.equipped).length:0)+' / 4', 'eq']
  ];
  return `<div class="attr-grid">${rows.map(([k,v])=>
    `<div class="attr-cell"><span class="ak">${k}</span><span class="av">${v}</span></div>`).join('')}</div>`;
}
function recentLogs(){
  const logs=(state&&state.logs)||[];
  if(!logs.length)return '';
  const rows=logs.slice(-3).reverse().map(l=>{
    const t=l.ts?new Date(l.ts*1000).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'}):'';
    return `<div class="log-row"><span class="lr-t">${t}</span><span class="lr-e">${l.event||''}${l.detail?' · '+l.detail:''}</span></div>`;
  }).join('');
  return `<div class="sec-title">最近动态</div><div class="log-list">${rows}</div>`;
}
const QUALITY=['普通','精良','稀有','史诗','传说','彩虹','暗影','混沌'];
// v1.9.27 中文名显示：装备=品质+部位，消耗品走 ITEM_CN 映射（杜绝内部 ID 乱码上屏）
const SLOT_CN={weapon:'武器',helmet:'头盔',armor:'护甲',trinket:'饰品'};
const ITEM_CN={potion_s:'回血药水',potion_l:'大瓶回血药水',药水:'回血药水',回血药水:'回血药水'};
// v1.9.38 武器分系：两套武器命名（按品质），穿戴/掉落/背包全链路一致
const WEAPON_NAMES={warrior:['木剑','铁剑','精钢战刃','骑士重剑','屠龙战刃','耀光圣剑','噬影魔刃','混沌·诸天剑'],mage:['学徒法杖','橡木法杖','符文法杖','贤者宝杖','龙魂法杖','星辉法杖','暗影权杖','混沌·万象杖']};
const WC_CN={warrior:'战士',mage:'法师'};
function wcTag(e){ if(!e||e.slot!=='weapon'||!e.classId)return''; const m=e.classId==='mage'?'✨':'⚔';
  return `<span class="wc-tag ${e.classId==='mage'?'m':'w'}">${m}${WC_CN[e.classId]}系</span>`; }
function eqCN(e){ if(!e)return'装备'; const q=Math.max(0,Math.min(7,e.quality||0));
  if(e.slot==='weapon'&&e.classId){ const nm=(WEAPON_NAMES[e.classId]||[])[q]; if(nm)return WC_CN[e.classId]+'系·'+nm; }
  return QUALITY[q]+(SLOT_CN[e.slot]||e.slot||'装备'); }
function itemCN(id){ return ITEM_CN[id]||id; }
// v1.9.28 查单件装备战力（来源：服务端 /api/stats/calc 的 equipPowers）
function pwOf(e){ if(!e||!heroStats||!heroStats.equipPowers)return null; const x=heroStats.equipPowers.find(p=>p.id===e.id); return x?Math.round(x.power):null; }
function pwBadge(e){ const pw=pwOf(e); return pw!=null?`<span class="eq-pw">战力 +${pw}</span>`:''; }
const QCOLOR=['#9aa7bd','#5fd38a','#5aa8e0','#b98cf0','#e8a33c','#5fe3c0','#8a5cf6','#ff5c8a'];

/* ============================================================
   面板渲染（单入口单出口，面板内 tab 不跨面板）
   ============================================================ */
function renderPanel(){
  if(panel==='role') return renderRole();
  if(panel==='home') return renderHome();
  if(panel==='bag') return renderBag();                         // v1.9.34 背包面板
  if(panel==='dungeon') return renderMap();                     // v1.9.34 副本已并入地图面板
  if(panel==='guild') return renderGuild();
  if(panel==='map') return renderMap();
  if(panel==='shop') return renderShop();                       // v1.9.53 商店面板
  // 兼容旧入口：仓库/挂机作为家园二级页
  if(panel==='warehouse') return renderWarehouse();
  if(panel==='idle') return renderIdle();
}
function tabRow(active){
  const tabs=[['info','角色'],['equip','装备栏'],['skill','技能'],['class','转职']];
  return `<div class="tab-row">${tabs.map(([k,n])=>`<button class="tab ${k===active?'on':''}" data-rtab="${k}">${n}</button>`).join('')}</div>`;
}

/* ---- 商店页（v1.9.53）：金币/钻石 → 强化材料 / 经验，兑换数量由玩家输入 ---- */
const SHOP_ITEMS=[
  {key:'material', pay:'gold', name:'强化材料', rate:'10 金币 / 件', per:10, def:10},
  {key:'material', pay:'gem',  name:'强化材料', rate:'1 钻石 / 件',  per:1,  def:10},
  {key:'exp',      pay:'gold', name:'经验',     rate:'1 金币 / 点',  per:1,  def:100},
  {key:'exp',      pay:'gem',  name:'经验',     rate:'1 钻石 / 10 点', per:0.1, def:100},
  // v1.9.68 强化材料兑回货币：pay='material' 表示消耗强化材料计数器，out=到手的货币
  {key:'materialToGold', pay:'material', out:'gold', name:'强化材料 → 金币', rate:'10 金币 / 件材料', per:1, gotPer:10, def:10},
  {key:'materialToGem',  pay:'material', out:'gem',  name:'强化材料 → 钻石', rate:'1 钻石 / 件材料',  per:1, gotPer:1,  def:10}
];
// 钻石兑经验 1:10，必须按 10 的倍数；其余商品按整数件/点
function shopNormQty(it, v){
  const n=Math.max(0,Math.floor(Number(v)||0));
  if(it.key==='exp'&&it.pay==='gem') return Math.max(10,Math.round(n/10)*10);
  return n;
}
function shopCostOf(it, qty){
  if(it.out) return qty; // 兑回卡：消耗的是强化材料件数
  return (it.key==='exp'&&it.pay==='gem') ? qty/10 : qty*it.per;
}
// 兑换卡文案：消耗什么 / 得到什么（v1.9.68 支持强化材料兑回）
function shopPayCN(it){ return it.pay==='gold'?'金币':it.pay==='gem'?'钻石':'强化材料'; }
function shopGotCN(it){ return it.out?(it.out==='gold'?'金币':'钻石'):(it.key==='material'?'强化材料':'经验'); }
function shopGotOf(it, qty){ return it.out ? qty*it.gotPer : qty; }
function renderShop(){
  $('panelTitle').textContent='商店';
  $('panelSub').textContent='金币/钻石兑换强化材料与经验 · 强化材料可兑金币(1:10)/钻石(1:1) · 背包物品可回收 · 数量自填';
  const c=(state&&state.core)||{};
  const cards=SHOP_ITEMS.map(it=>{
    const id='sq_'+it.key+'_'+it.pay;
    const have=it.pay==='gold'?c.gold:it.pay==='gem'?c.gem:(c.material||0);
    const own=it.out?(it.out==='gold'?c.gold:c.gem):(it.key==='material'?c.material:c.exp);
    const ownCN=it.out?(it.out==='gold'?'金币':'钻石'):(it.key==='material'?'材料':'经验');
    return `<div class="shop-card">
      <div class="sc-head"><span class="sc-name">${it.name}</span><span class="sc-rate">${it.rate}</span></div>
      <div class="sc-have">持有 <b style="color:#f0d590">${fmt(have)}</b> ${shopPayCN(it)} · 现有${ownCN} ${fmt(own)}</div>
      <div class="sc-buy">
        <input id="${id}" type="number" min="1" max="1000000" step="${it.key==='exp'&&it.pay==='gem'?10:1}" value="${it.def}" inputmode="numeric">
        <button data-shop="${it.key}|${it.pay}">兑换</button>
      </div>
      <div class="sc-cost" id="${id}_cost"></div>
    </div>`;
  }).join('');
  // v1.9.56 背包回收：材料 / 药水 → 金币或钻石（单价读 CFG.economy.shop.sell）
  const sellList=sellableList();
  const sellRows=sellList.map(([id,qty],i)=>{
    const kind=(state.warehouse||[]).find(it=>it.id===id)?.kind||'材料';
    const g=sellPriceOf(id,kind,'gold'), m=sellPriceOf(id,kind,'gem');
    return `<div class="sell-row">
      <div class="sr-main"><span class="sr-name">${itemCN(id)}</span><span class="sr-qty">×${fmt(qty)}</span></div>
      <div class="sr-price">${fmt(g)} 金 / ${fmt(m)} 钻 · 每件</div>
      <input id="sellq_${i}" type="number" min="1" max="${qty}" step="1" value="1" inputmode="numeric">
      <button class="sr-btn" data-sell="${i}|gold">收金币</button>
      <button class="sr-btn sr-btn-gem" data-sell="${i}|gem">收钻石</button>
    </div>`;
  }).join('');
  window._sellList=sellList;
  $('panelBody').innerHTML=`<div class="sec-title">兑换商店</div>
    <div class="sec-sub" style="margin-bottom:6px">强化材料 10 金币/件 或 1 钻石/件；经验 1 金币/点 或 1 钻石/10 点（钻石档按 10 的倍数）；强化材料兑回：1 件 = 10 金币 或 1 钻石（v1.9.68）</div>
    <div class="shop-grid">${cards}</div>
    <div class="shop-tip">兑换即时到账：强化材料计入角色材料数（强化/升品时消耗）；经验直接加到当前等级进度，攒够会自动升级。</div>
    <div class="sec-title" style="margin-top:16px">背包回收（材料/药水 · 可回收物品 → 金币或钻石）</div>
    <div class="sec-sub" style="margin-bottom:6px">按件计价，卖出即时到账；含初始背包里的秘银、金币袋。装备请到家园›仓库分解，不在本列表。</div>
    <div class="sell-grid">${sellRows||'<div class="sec-sub">背包里没有可回收的材料或药水</div>'}</div>`;
  SHOP_ITEMS.forEach(it=>{
    const id='sq_'+it.key+'_'+it.pay;
    const inp=$(id), costEl=$(id+'_cost'), btn=inp&&inp.parentElement.querySelector('button');
    if(!inp||!costEl||!btn) return;
    const upd=()=>{
      const qty=shopNormQty(it, inp.value), cost=shopCostOf(it, qty), got=shopGotOf(it, qty);
      const have=it.pay==='gold'?c.gold:it.pay==='gem'?c.gem:(c.material||0);
      costEl.innerHTML=`消耗 <b>${fmt(cost)}</b> ${shopPayCN(it)} · 可得 <b>${fmt(got)}</b> ${shopGotCN(it)}`;
      btn.disabled=!(qty>0)||cost>have;
    };
    inp.oninput=upd;
    inp.onchange=()=>{ if(it.key==='exp'&&it.pay==='gem') inp.value=shopNormQty(it,inp.value); upd(); };
    btn.onclick=()=>shopBuy(it, inp);
    upd();
  });
  // v1.9.56 回收：数量自填，超出持有量前端先拦一层，服务端再兜一次
  $('panelBody').querySelectorAll('.sr-btn[data-sell]').forEach(b=>b.onclick=()=>{
    const [idx,pay]=b.dataset.sell.split('|');
    const row=(window._sellList||[])[Number(idx)];
    if(!row) return;
    const inp=$('sellq_'+idx);
    const qty=Math.floor(Number(inp&&inp.value)||0);
    shopSell(row[0], pay, qty, row[1]);
  });
}
// 可回收物品聚合：仓库材料 / 药水 + 初始背包（ext.bag）材料，按 id 合并堆叠
// v1.9.57：ext.bag 里的秘银、金币袋 原本不在仓库列表里，导致商店看不到也卖不掉；一并并入
function sellableList(){
  const agg={};
  (state.warehouse||[]).forEach(it=>{
    if(!it||!it.id||!(it.qty>0)) return;
    const k=it.kind;
    if(k!=='材料'&&k!=='material'&&k!=='consume'&&k!=='药水'&&k!=='potion') return;
    agg[it.id]=(agg[it.id]||0)+it.qty;
  });
  const bag=(state&&state.ext&&state.ext.bag)||{};
  Object.entries(bag).forEach(([id,n])=>{
    const q=Math.floor(Number(n)||0);
    if(!id||q<=0) return;
    agg[id]=(agg[id]||0)+q;
  });
  return Object.entries(agg);
}
// 单件回收价：优先按 id 定价，其次按类别兜底（与服务端同一套规则）
function sellPriceOf(id,kind,pay){
  const sell=(CFG&&CFG.economy&&CFG.economy.shop&&CFG.economy.shop.sell)||{};
  const row=(sell.items||{})[id];
  if(row&&Number(row[pay])>0) return Number(row[pay]);
  const g=(kind==='consume'||kind==='药水'||kind==='potion')?'consume':'material';
  const def=((sell.default||{})[g]||{});
  if(Number(def[pay])>0) return Number(def[pay]);
  return pay==='gold'?(g==='consume'?150:100):10; // v1.9.57 兜底价 ×10，与后端一致
}
// 物品中文名：装备之外的中文 id 直接用，内部 id 走 ITEM_CN
function itemCN(id){ return ITEM_CN[id]||id; }
async function shopSell(itemId,pay,qty){
  if(!(qty>0)){ toastModal('回收失败','请输入大于 0 的回收数量'); return; }
  try{
    const r=await api('/api/player/action',{userId:uid(),action:'shop_sell',itemId,payWith:pay,qty});
    if(r.state) state=r.state;
    window.SFX&&SFX.coin();
    syncHud();
    await refreshStats();
    renderShop();
    toastModal('回收成功',`${itemCN(itemId)} ×${fmt(r.qty||qty)} → ${pay==='gold'?'金币':'钻石'} +${fmt(r.got)}`);
  }catch(e){
    window.SFX&&SFX.fail();
    toastModal('回收失败',(e&&e.message)||String(e));
  }
}
async function shopBuy(it, inp){
  const qty=shopNormQty(it, inp.value);
  if(!(qty>0)){ toastModal('兑换失败','请输入大于 0 的兑换数量'); return; }
  inp.value=qty;
  try{
    // 服务端权威：比例、扣费、入账都在后端算，前端只报商品/货币/数量
    const r=await api('/api/player/action',{userId:uid(),action:'shop_exchange',shopItem:it.key,payWith:it.pay,qty});
    if(r.state) state=r.state;
    window.SFX&&SFX.coin();
    await applyStats(r.stats);
    renderShop();
    toastModal('兑换成功',`消耗 ${fmt(r.cost)} ${shopPayCN(it)} → ${shopGotCN(it)} +${fmt(r.got)}`);
  }catch(e){
    window.SFX&&SFX.fail();
    toastModal('兑换失败', (e&&e.message)||String(e));
  }
}

/* ---- 家园页（v1.6）：聚合 仓库 + 挂机 两个二级入口 ---- */
function renderHome(){
  $('panelTitle').textContent='家园';
  $('panelSub').textContent='家园装饰与功能入口 · 点击卡片进入对应子页';
  const body=$('panelBody');
  if(homeSub==='warehouse') return renderWarehouse(true);
  if(homeSub==='idle') return renderIdle(true);
  const used=(state?.warehouse?.length)||0;
  const homeInfo=state?.home||{cart:0,bed:0};
  const cartLv=homeInfo.cart|0, bedLv=homeInfo.bed|0;
  const capBase=200, capAdd=10, idleBase=8, idleAdd=6;
  const cartCost=800*(cartLv+1), bedCost=1000*(bedLv+1);
  const gold=state?.core?.gold||0;
  const deco=(id,ico,name,lv,effect,effNext,cost,cls)=>`
      <button class="deco-item ${cls}" data-deco="${id}" title="升级消耗 ${fmt(cost)} 金币">
        <span class="deco-ico">${ico(40)}</span>
        <span class="deco-name">${name} <em>Lv.${lv}</em></span>
        <span class="deco-eff">${effect}</span>
        <span class="deco-up ${gold>=cost?'':'no'}">${ICON.gold(11,'#f0c040')}${fmt(cost)}<i>→${effNext}</i></span>
      </button>`;
  const look=(state&&state.appearance&&state.appearance.homeLook)||{};
  const cdef2=(CFG.classes.classes||[]).find(x=>x.id===(state&&state.core&&state.core.classId))||{};
  const lookTxt=(window.Look&&Look.describe)?Look.describe(look):'';
  body.innerHTML=`
    <div class="home-avatar">
      <canvas id="homeAvatar" width="128" height="160"></canvas>
      <div class="ha-txt">
        <div class="ha-name">${(state&&state.core&&state.core.name)||'英雄'} · ${cdef2.name||''}</div>
        <div class="ha-sub">家园休闲装 · 6 部件自由搭配（4⁶ = 4096 种）</div>
        <div class="ha-look">${lookTxt||'尚未设置外观'}</div>
      </div>
      <button class="ha-btn" id="hcDress">换 装</button>
    </div>
    <div class="home-scene" style="background-image:linear-gradient(180deg,rgba(8,13,22,.30),rgba(8,13,22,.90)),url(${RES}v3/backgrounds/bg_explore_grassland.png)">
      ${deco('cart',ICON.cart,'手推车',cartLv,`仓库容量 +${cartLv*capAdd} 格`,`+${capAdd} 格`,cartCost,'dc-cart')}
      ${deco('bed', ICON.bed, '营地床',bedLv, `挂机时长 +${bedLv*idleAdd}%`,`+${idleAdd}%`,bedCost,'dc-bed')}
    </div>
    <div class="home-cards">
      <button class="home-card hc-warehouse" id="hcWarehouse">
        <span class="hc-ico">${ICON.bag(26)}</span><span class="hc-title">仓库</span>
        <span class="hc-desc">已用 ${used} / ${capBase+cartLv*capAdd} 格</span><span class="hc-enter">进入</span>
      </button>
      <button class="home-card hc-idle" id="hcIdle">
        <span class="hc-ico">${ICON.hourglass(26)}</span><span class="hc-title">挂机收益</span>
        <span class="hc-desc">离线上限 ${(idleBase*(1+bedLv*idleAdd/100)).toFixed(1)}h · 可领取</span><span class="hc-enter">进入</span>
      </button>
    </div>
    <div class="home-note">升级可提升仓库容量与离线收益上限 · 每次升级费用递增，上限 Lv.20</div>`;
  if(window.Look&&$('homeAvatar'))Look.renderInto($('homeAvatar'),look,8);
  const dressBtn=$('hcDress');
  if(dressBtn)dressBtn.onclick=()=>{
    if(!window.Creator)return;
    window.SFX&&SFX.tab();
    Creator.open({
      mode:'look', cfg:CFG, api:api, uid:uid(), sfx:window.SFX, toast:toastModal,
      look:(state&&state.appearance&&state.appearance.homeLook)||{}, classId:state.core.classId,
      name:(state.core&&state.core.name)||'',
      onDone:(st)=>{ state=st; syncHud(); renderHome(); toastModal('换装完成','家园外观已更新'); }
    });
  };
  $('hcWarehouse').onclick=()=>{homeSub='warehouse';window.SFX&&SFX.tab();renderHome();};
  $('hcIdle').onclick=()=>{homeSub='idle';window.SFX&&SFX.tab();renderHome();};
  body.querySelectorAll('.deco-item').forEach(el=>el.onclick=async()=>{
    const id=el.dataset.deco, name=id==='cart'?'手推车':'营地床';
    const lv=(id==='cart'?cartLv:bedLv)+1;
    const cost=r=>r; // 服务端定价，前端只展示
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'home_decorate',itemId:id});
      if(r.state){state=r.state;syncHud();}
      if(typeof SFX!=='undefined')SFX.build();
      toastModal('装饰升级',`${name} 提升至 Lv.${lv}（消耗 ${fmt(r.cost||0)} 金币）`);
      renderHome();
    }catch(e){
      if(typeof SFX!=='undefined')SFX.fail();
      toastModal('升级失败',e.message);
    }
  });
}

/* ---- 副本页（v1.6）：副本列表 + 进入战斗 ---- */
const DUNGEONS=[
  {id:'grass',name:'青草试炼',lv:1,perDay:3,drop:'掉落基础材料',mon:'slime',elite:false},
  {id:'mist', name:'迷雾回廊',lv:10,perDay:2,drop:'掉落强化材料',mon:'bat',elite:false},
  {id:'flame',name:'赤焰深渊',lv:20,perDay:2,drop:'掉落装备',mon:'skeleton',elite:true},
  {id:'frost',name:'霜冻之巅',lv:30,perDay:1,drop:'掉落稀有材料',mon:'boss_beetle',elite:true},
];
/* ---- 背包页（v1.9.34）：随时可开；存取仓库仅在家园 ---- */
function renderBag(){
  $('panelTitle').textContent='背包';
  const body=$('panelBody');
  const inHome=worldMap&&worldMap.worldId==='HOME';
  const eqAll=state&&state.equip?state.equip:[];
  const bagEq=eqAll.filter(e=>!e.equipped);
  const bagGoods=Object.entries((state&&state.ext&&state.ext.bag)||{}).filter(([k,v])=>(v|0)>0);
  const slotIco={weapon:ICON.sword(24,'#2a1c08'),helmet:ICON.helmet(24,'#2a1c08'),armor:ICON.armor(24,'#2a1c08'),trinket:ICON.ring(24,'#2a1c08')};
  const isPotion=(id)=>id==='potion_s'||id==='potion_l'||id==='药水'||id==='回血药水';
  // v1.9.37：药水独立格子区（仓库+背包合并计数，任意地点点击直接喝）
  const potMap={};
  (state.warehouse||[]).forEach(it=>{ if(it.kind==='consume'&&isPotion(it.id)&&it.qty>0)potMap[it.id]=(potMap[it.id]||0)+it.qty; });
  bagGoods.forEach(([id,n])=>{ if(isPotion(id))potMap[id]=(potMap[id]||0)+n; });
  const potList=Object.entries(potMap);
  const matGoods=bagGoods.filter(([id])=>!isPotion(id));
  body.innerHTML=`
    ${inHome?'<button class="big-btn goldbtn" id="openWh" style="width:100%">📦 仓库管理（家园限定 · 存入/取出装备与材料）</button>':'<div class="sec-sub" style="color:#9fb0c8">🏠 存入仓库与仓库管理需回家园操作 · 背包查看/穿戴/用药随时可用</div>'}
    <div class="wh-confirm hidden" id="bagConfirm">
      <span class="wh-c-name" id="bagCName"></span>
      <input id="bagQtyInput" type="number" min="1" value="1">
      <button class="wh-mini btn-gray" id="bagAll">全部</button>
      <button class="wh-mini btn-gray" id="bagNo">取消</button>
      <button class="wh-mini wh-ok" id="bagYes">确定</button>
    </div>
    <div class="sec-title">背包 · 装备 <span class="sec-sub" style="display:inline">（未穿戴 ${bagEq.length} 件 · ${inHome?'点击装备存入仓库':'点击装备直接穿戴'}）</span></div>
    <div class="wg-grid">${bagEq.map(e=>`
      <div class="wg-cell equip" data-wear="${e.id}" data-in="${inHome?e.id:''}">
        <div class="wgi" style="background:#b8862f">${slotIco[e.slot]||ICON.sword(24,'#2a1c08')}</div>${eqCN(e)} +${e.enhance||0}<div class="wq">x1</div>
        ${wcTag(e)||''}
        ${pwBadge(e)||''}
        <span class="wg-detail" data-detail="${e.slot}">详情</span>
      </div>`).join('')||'<div class="sec-sub">背包暂无未穿戴装备</div>'}</div>
    <div class="sec-title">背包 · 药水 <span class="sec-sub" style="display:inline">（${potList.length} 种 · 点击直接喝 · 任意地点可用）</span></div>
    <div class="wg-grid">${potList.map(([id,n])=>`
      <div class="wg-cell pot" data-pot="${id}"><div class="wgi" style="background:#5fd38a">${ICON.crystal(20,'#2a1c08')}</div>${itemCN(id)}<div class="wq">x${n}</div></div>`).join('')||'<div class="sec-sub">暂无药水 · 开宝箱/采集草药可获得</div>'}</div>
    <div class="sec-title">背包 · 可回收物品 <span class="sec-sub" style="display:inline">（${matGoods.length} 种 · ${inHome?'点击存入仓库':'材料存取需回家园'}）</span></div>
    <div class="wg-grid">${matGoods.map(([id,n])=>`
      <div class="wg-cell" data-g="${id}" data-n="${n|0}"><div class="wgi" style="background:#8892a4">${ICON.crystal(20,'#2a1c08')}</div>${itemCN(id)}<div class="wq">x${n|0}</div></div>`).join('')||'<div class="sec-sub">背包暂无材料</div>'}</div>
    <div class="wh-flash hidden" id="bagFlash"></div>`;
  const flash=(msg)=>{ const f=$('bagFlash'); if(!f)return; f.textContent=msg; f.classList.remove('hidden');
    clearTimeout(f._t); f._t=setTimeout(()=>f.classList.add('hidden'),2200); };
  // 装备：家园=存入仓库；大世界=穿戴
  body.querySelectorAll('.wg-cell[data-wear]').forEach(el=>el.onclick=async(ev)=>{
    if(ev.target.classList.contains('wg-detail'))return;
    const id=el.dataset.wear;
    if(inHome&&el.dataset.in){
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'wh_equip_in',itemId:id});
        if(r.state){state=r.state;syncHud();}
        window.SFX&&SFX.bagIn(); renderPanel();
        flash('已存入仓库 '+(eqCN((state.equip||[]).find(x=>x.id===id))||id));
      }catch(e){window.SFX&&SFX.fail();flash('存入失败：'+e.message);}
      return;
    }
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'equip',itemId:id});
      if(r.state){state=r.state;await applyStats(r.stats);}
      window.SFX&&SFX.equip(); renderPanel();
      flash('已穿戴 '+(eqCN((state.equip||[]).find(x=>x.id===id))||id));
    }catch(e){window.SFX&&SFX.fail();flash('穿戴失败：'+e.message);}
  });
  // 详情角标（跳角色-装备栏详情）
  body.querySelectorAll('.wg-detail[data-detail]').forEach(el=>el.onclick=ev=>{
    ev.stopPropagation();
    equipDetailSlot=el.dataset.detail; window.SFX&&SFX.tab();
    panel='role'; roleTab='equip'; renderRole();
    $('panelTitle').textContent='角色';
  });
  // 材料/消耗品：家园=数量确认存入；药水随时可使用；非家园材料点击仅提示
  const openConfirm=(id,max,name)=>{
    $('bagCName').textContent=name+'（持有 '+max+'）';
    const inp=$('bagQtyInput'); inp.value=1; inp.max=max;
    $('bagConfirm').classList.remove('hidden'); inp.focus();
    bagOp={id,max,name};
  };
  let bagOp=null;
  $('bagAll').onclick=()=>{ if(bagOp)$('bagQtyInput').value=bagOp.max; };
  $('bagNo').onclick=()=>{ bagOp=null; $('bagConfirm').classList.add('hidden'); };
  $('bagYes').onclick=async()=>{
    if(!bagOp)return;
    const q=Math.max(1,Math.min(bagOp.max,parseInt($('bagQtyInput').value)||1));
    const op=bagOp; bagOp=null; $('bagConfirm').classList.add('hidden');
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'deposit',itemId:op.id,qty:q});
      if(r.state){state=r.state;syncHud();}
      window.SFX&&SFX.bagIn(); renderPanel();
      flash('已存入仓库 '+(op.name||itemCN(op.id))+' ×'+q);
    }catch(e){window.SFX&&SFX.fail();flash('存入失败：'+e.message);}
  };
  $('bagQtyInput').onkeydown=(e)=>{ if(e.key==='Enter')$('bagYes').click(); };
  // 药水：点击直接喝（v1.9.37：仓库/背包合并计数，任意地点可用，服务端先扣仓库再扣背包）
  // v1.9.41：满血本地预检（不发请求）+ 去掉多余 refreshStats（use_potion 不改属性，降交互延迟）
  body.querySelectorAll('.wg-cell[data-pot]').forEach(el=>el.onclick=async()=>{
    const id=el.dataset.pot;
    const maxHp=(heroStats&&heroStats.hp)||0;
    const hp=(state&&state.core&&typeof state.core.hp==='number'&&state.core.hp>0)?state.core.hp:maxHp;
    if(maxHp>0&&hp>=maxHp){ window.SFX&&SFX.fail(); flash('血量已满（'+Math.ceil(hp)+'/'+Math.ceil(maxHp)+'），无需喝药水'); return; }
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'use_potion',itemId:id});
      if(r.state){state=r.state;syncHud();}
      window.SFX&&SFX.heal(); renderPanel();
      flash('已喝下 '+(itemCN(id)||id)+' · 回复 '+Math.round(r.healed||0)+' 血量（当前 '+Math.round(r.hp||0)+'/'+Math.round(r.maxHp||0)+'）');
    }catch(e){window.SFX&&SFX.fail();flash('无法使用：'+e.message);}
  });
  // 材料：家园=数量确认存入；非家园材料点击仅提示
  body.querySelectorAll('.wg-cell[data-g]').forEach(el=>el.onclick=async()=>{
    const id=el.dataset.g, n=el.dataset.n|0;
    if(inHome){ openConfirm(id,n,itemCN(id)); return; }
    flash(itemCN(id)+' ×'+n+' · 材料存取需回家园操作');
  });
  const whBtn=$('openWh');
  if(whBtn)whBtn.onclick=()=>{ homeSub='warehouse'; panel='home'; renderHome(); $('panelTitle').textContent='家园'; window.SFX&&SFX.tab(); };
}

/* ---- 公会页（v1.9.2）：捐献 / 公会商店 / 成员，服务端权威 ---- */
const GUILD_SHOP=[
  {id:'gem_pack',      name:'钻石礼包 ×60',  price:200, ico:'gem'},
  {id:'material_pack', name:'材料包 ×30',    price:150, ico:'bag'},
  {id:'skill_book',    name:'奥义书（经验+300）', price:300, ico:'book'}
];
// 公会（v1.9.39 大改）：分流服务端权威化 —— /api/guild/list 返回 my（我所在公会）。
// 已加入 → 点击直接进入"我的公会"主界面（不再依赖客户端 state.guild，老档丢失也能正确识别）；
// 未加入 → 搜索/加入界面（搜索框按公会名过滤）。
const GUILD_ROLE_TXT={president:'会长',officer:'副会长',member:'成员'};
async function renderGuild(){
  $('panelTitle').textContent='公会';
  const body=$('panelBody');
  let detail;
  try{ detail=await api('/api/guild/list',{userId:uid()}); }
  catch(e){ body.innerHTML='<div class="sec-sub">公会数据加载失败：'+e.message+'</div>'; return; }
  const my=detail.my||null;
  if(!my){
    // 未加入（或公会已解散）：服务端查不到我所在公会 → 搜索界面
    if(state&&state.guild){ state.guild=null; syncHud(); }
    await renderGuildJoin(detail);
    return;
  }
  // —— 已加入：公会主界面（同步修复客户端 state.guild 丢失问题） ——
  state.guild={id:my.id,name:my.name,role:detail.myRole||'member'};syncHud();
  const gv=my;
  const ext=(state&&state.ext)||{};
  const coin=Math.floor(ext.guildCoin||0);
  const donated=Math.floor(ext.guildDonated||0);
  const lv=gv.level||1, prog=gv.progress||0;
  const nextLv=prog-(lv-1)*1000;
  const members=(gv.members||[]).slice().sort((a,b)=>(a.role==='president'?-1:0)-(b.role==='president'?-1:0)||b.donate-a.donate);
  const president=(gv.members||[]).find(m=>m.role==='president');
  const isPresident=(detail.myRole||'member')==='president';
  body.innerHTML=`
    <div class="guild-hero">
      <div class="gh-emblem">${ICON.shield(40,'#f0d590')}</div>
      <div class="gh-meta">
        <div class="gh-name">${gv.name} <em>Lv.${lv}</em>${isPresident?'<span class="cl-req ok" style="margin-left:8px">我的公会</span>':''}</div>
        <div class="gh-sub">会长 ${president?president.name:'-'} · 成员 ${gv.size}/30 · 创建于 ${new Date(gv.createdAt*1000).toLocaleDateString()}</div>
        <div class="gh-bar"><i style="width:${Math.min(100,nextLv/10)}%"></i><span>公会进度 ${prog}（距 Lv.${lv+1} 还需 ${1000-nextLv}）</span></div>
      </div>
      <div class="gh-coin"><em>公会币</em><b>${fmt(coin)}</b></div>
    </div>
    <div class="sec-title">公会捐献</div>
    <div class="guild-donate">
      <div class="gd-info">
        <div class="gd-t">每日捐献 <b>${donated}/3</b></div>
        <div class="gd-s">消耗 600 金币 → 个人获 500 公会币 + 600 经验，公会进度 +50</div>
      </div>
      <button class="big-btn goldbtn" id="guildDonate" ${donated>=3?'disabled style="opacity:.5;cursor:not-allowed"':''}>${donated>=3?'今日已满':'捐 献'}</button>
    </div>
    <div class="sec-title">公会商店</div>
    <div class="guild-shop">
      ${GUILD_SHOP.map(it=>`
        <div class="gs-item">
          <span class="gs-ico">${ICON[it.ico]?ICON[it.ico](24,'#f0d590'):''}</span>
          <div class="gs-main"><div class="gs-name">${it.name}</div><div class="gs-price">${ICON.gold(12,'#c9a227')} ${it.price} 公会币</div></div>
          <button class="gs-buy" data-item="${it.id}" ${coin<it.price?'disabled style="opacity:.45"':''}>兑换</button>
        </div>`).join('')}
    </div>
    <div class="sec-title">成员（${gv.size}/30）</div>
    <div class="guild-members">
      ${members.map(m=>{
        const me=m.userId===uid();
        return `<div class="gm-row ${me?'gm-me':''}"><span class="gm-dot ${m.userId===uid()?'on':''}"></span>
          <span class="gm-name">${m.name}${me?'（你）':''}</span>
          <span class="gm-power">⚔ ${fmt(m.power||0)}</span>
          <span class="gm-role ${m.role==='president'?'r-p':m.role==='officer'?'r-o':''}">${GUILD_ROLE_TXT[m.role]||m.role}</span>
          <span class="gm-lv">Lv.${m.level}</span>
          <button class="gm-pf" data-uid="${m.userId}" data-name="${m.name}" title="查看该成员档案">档案</button></div>`;
      }).join('')}
    </div>
    <div style="margin-top:12px;display:flex;gap:10px">
      <button class="btn-gray" id="guildLeave">${isPresident?'退出公会（转让给最早加入的成员）':'退出公会'}</button>
      <span class="sec-sub" style="flex:1;text-align:right;align-self:center">捐献与商店每日 0 点重置</span>
    </div>`;
  $('panelSub').textContent=`${gv.name} · Lv.${lv} 公会`;
  // v1.9.61 公会成员行加"档案"按钮：点开看该成员的完整档案
  body.querySelectorAll('.gm-pf').forEach(b=>{
    b.onclick=(ev)=>{ ev.stopPropagation(); askPlayerProfile({userId:b.dataset.uid,name:b.dataset.name}); };
  });  $('guildDonate').onclick=async()=>{
    try{
      const r=await api('/api/player/action',{userId:uid(),action:'guild_donate'});
      if(r.state){state=r.state;await applyStats(r.stats);}
      window.SFX&&SFX.donate();
      toastModal('捐献成功','公会币 +500 · 经验 +600 · 公会进度 +50');
      renderGuild();
    }catch(e){ window.SFX&&SFX.fail(); toastModal('捐献失败',e.message); }
  };
  body.querySelectorAll('.gs-buy').forEach(b=>{
    if(b.disabled)return;
    b.onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'guild_buy',itemId:b.dataset.item});
        if(r.state){state=r.state;await applyStats(r.stats);}
        window.SFX&&SFX.buy();
        toastModal('兑换成功',GUILD_SHOP.find(x=>x.id===b.dataset.item).name);
        renderGuild();
      }catch(e){ window.SFX&&SFX.fail(); toastModal('兑换失败',e.message); }
    };
  });
  $('guildLeave').onclick=()=>{
    showModal(`<h2>退出公会</h2><div class="msub">${isPresident?'你是会长：退出后公会将转让给最早加入的成员；若无其他成员则解散。':'确定退出公会 '+gv.name+' ？退出后公会币保留，可随时加入新公会。'}</div>
      <div class="divider"></div>
      <div class="enc-btns"><button class="btn-red" id="doLeave">确定退出</button><button class="btn-gray" id="cancelLeave">再想想</button></div>`);
    $('doLeave').onclick=async()=>{
      try{
        const r=await api('/api/guild/leave',{userId:uid()});
        if(r.state){state=r.state;syncHud();}
        hideModal(); window.SFX&&SFX.tab();
        toastModal('已退出公会',r.message||'');
        renderGuild();
      }catch(e){ hideModal(); toastModal('退出失败',e.message); }
    };
    $('cancelLeave').onclick=hideModal;
  };
}
// 未加入：搜索加入为主（v1.9.39：搜索框按公会名实时过滤），创建公会入口保留
async function renderGuildJoin(preloaded){
  $('panelSub').textContent='搜索并加入公会，或创建自己的公会';
  const body=$('panelBody');
  const myGold=state&&state.core?state.core.gold:0;
  body.innerHTML=`
    <div class="guild-hero">
      <div class="gh-emblem">${ICON.shield(40,'#5a6b80')}</div>
      <div class="gh-meta">
        <div class="gh-name">你还没有加入公会</div>
        <div class="gh-sub">加入公会后可每日捐献换公会币，在公会商店兑换钻石礼包 / 材料包 / 奥义书</div>
      </div>
    </div>
    <div class="sec-title">搜索公会 <button class="btn-gray" id="guildRefresh" style="margin-left:10px;padding:3px 12px">刷新</button></div>
    <div style="display:flex;gap:8px;margin:6px 0 10px">
      <input id="guildSearch" placeholder="输入公会名搜索（留空显示全部）" style="flex:1;background:#1b2436;color:#e8e8e8;border:1px solid #3a4a63;padding:7px 10px;border-radius:8px">
    </div>
    <div id="guildList" class="sec-sub">正在加载公会列表…</div>
    <div class="sec-title">创建公会</div>
    <div class="guild-donate">
      <div class="gd-info">
        <div class="gd-t">创建消耗 <b>5,000 金币</b>（当前 ${fmt(myGold)}）</div>
        <div class="gd-s">你将成为会长 · 公会名 2~12 字符 · 上限 30 人</div>
      </div>
      <div style="display:flex;gap:8px;align-items:center">
        <input id="guildName" placeholder="输入公会名" maxlength="12" style="width:150px;background:#1b2436;color:#e8e8e8;border:1px solid #3a4a63;padding:7px 10px;border-radius:8px">
        <button class="big-btn goldbtn" id="guildCreateBtn" ${myGold<5000?'disabled style="opacity:.5"':''}>创 建</button>
      </div>
    </div>`;
  let gs=[];
  const renderRows=()=>{
    const kw=(($('guildSearch')||{}).value||'').trim().toLowerCase();
    const hit=kw?gs.filter(x=>(x.name||'').toLowerCase().includes(kw)):gs;
    const el=$('guildList');
    if(!el)return;
    el.className='';
    el.innerHTML=hit.length?hit.map(x=>{
      const pres=(x.members||[]).find(m=>m.role==='president');
      return `<div class="gr-row">
        <div class="gr-emb">${ICON.shield(22,'#f0d590')}</div>
        <div class="gr-main"><div class="gr-name">${x.name} <em>Lv.${x.level}</em></div>
          <div class="gr-sub">会长 ${pres?pres.name:'-'} · ${x.size}/30 人 · 进度 ${x.progress}</div></div>
        <button class="btn-gray gr-join" data-gid="${x.id}" data-gname="${x.name}" ${x.size>=30?'disabled style="opacity:.45"':''}>${x.size>=30?'已满员':'加入'}</button>
      </div>`;
    }).join(''):(kw?'没有名字包含「'+kw+'」的公会':'暂无公会，创建第一个吧！');
    el.querySelectorAll('.gr-join').forEach(b=>b.onclick=async()=>{
      try{
        const r2=await api('/api/guild/join',{userId:uid(),guildId:b.dataset.gid});
        if(r2.state){state=r2.state;syncHud();}
        window.SFX&&SFX.buy();
        toastModal('加入成功','欢迎加入 '+b.dataset.gname);
        renderGuild();
      }catch(e){ window.SFX&&SFX.fail(); toastModal('加入失败',e.message); }
    });
  };
  const loadList=async()=>{
    try{
      const r=preloaded||await api('/api/guild/list',{userId:uid()});
      preloaded=null;               // 只复用一次，之后点刷新重新请求
      gs=r.guilds||[];
      renderRows();
    }catch(e){ const el=$('guildList'); if(el)el.textContent='列表加载失败：'+e.message; }
  };
  await loadList();
  $('guildSearch').oninput=renderRows;
  $('guildRefresh').onclick=loadList;
  $('guildCreateBtn').onclick=async()=>{
    const name=$('guildName').value.trim();
    if(!name){ toastModal('提示','请输入公会名（2~12 字符）'); return; }
    try{
      const r=await api('/api/guild/create',{userId:uid(),name});
      if(r.state){state=r.state;await applyStats(r.stats);}
      window.SFX&&SFX.transfer();
      toastModal('公会创建成功','「'+name+'」的成员们等你发号施令！');
      renderGuild();
    }catch(e){ window.SFX&&SFX.fail(); toastModal('创建失败',e.message); }
  };
}
/* 角色页"装备概览"：4 槽横向小卡，避免信息页下半屏留白 */
function quickEquip(eq){
  const order=[['weapon','武器',ICON.sword],['helmet','头盔',ICON.helmet],
               ['armor','护甲',ICON.armor],['trinket','饰品',ICON.ring]];
  const cells=order.map(([slot,cn,ico])=>{
    const e=(eq||[]).find(x=>x.slot===slot&&x.equipped);
    const q=e?Math.max(0,Math.min(4,e.quality||0)):-1;
    return `<div class="qeq ${e?'on':''}" style="${e?'border-color:'+QCOLOR[q]:''}">
      <div class="qeq-i">${ico(22,e?QCOLOR[q]:'#66748c')}</div>
      <div class="qeq-t">${cn}</div>
      <div class="qeq-s" style="${e?'color:'+QCOLOR[q]:''}">${e?('+'+((e.enhance|0))):'空'}</div>
      ${e?pwBadge(e):''}</div>`;
  }).join('');
  return `<div class="sec-title">装备概览</div><div class="qeq-grid">${cells}</div>`;
}
function renderRole(){
  const body=$('panelBody');
  if(!state||!state.core){body.innerHTML='<div class="sec-sub">存档未就绪</div>';return;}
  const c=state.core;
  const cdef=CFG.classes.classes.find(x=>x.id===c.classId);
  let html=tabRow(roleTab);
  if(roleTab==='info'){
    const eq=state.equip||[];
    const done=eq.filter(e=>e.equipped).length;
    const need=expNeeded(c.level), exp=Math.max(0,c.exp||0), pct=Math.min(100,need>0?exp/need*100:0);
    html+=`
      <div class="hero-card">
        <div class="hero-portrait"><img src="${RES}v3/characters/cards/${(cdef&&cdef.icon)||'ui_card_mage.png'}" alt=""></div>
        <div class="hero-brief">
          <div class="hb-name">${c.name||'英雄'}<em>${cdef?cdef.name:c.classId}</em></div>
          <div class="hb-lv">Lv.${c.level}<span>已装备 ${done}/4</span></div>
          <div class="hb-exp"><i style="width:${pct.toFixed(1)}%"></i><span>${fmt(exp)} / ${fmt(need)}</span></div>
        </div>
        <div class="hero-power"><em>战力</em><b>${fmt((heroStats&&heroStats.power)||c.battlePower)}</b></div>
      </div>
      <div class="sec-title">基础属性</div>${attrRows()}
      ${quickEquip(eq)}
      ${recentLogs()}`;
  } else if(roleTab==='equip'){
    const eq=state.equip||[];
    const slotNames={weapon:'武器',helmet:'头盔',armor:'护甲',trinket:'饰品'};
    const slotIco={weapon:ICON.sword(30),helmet:ICON.helmet(30),armor:ICON.armor(30),trinket:ICON.ring(30)};
    const order=['weapon','helmet','armor','trinket'];
    const cells=order.map(id=>{
      const e=eq.find(x=>x.slot===id&&x.equipped);
      const q=e?Math.max(0,Math.min(4,e.quality||0)):-1;
      return `<div class="equip-cell ${e?'worn':''}" style="${e?'border-color:'+QCOLOR[q]:''}">
        <div class="ei">${slotIco[id]}</div><div class="en">${slotNames[id]}</div>
        <div class="el" style="${e?'color:'+QCOLOR[q]:''}">${e?eqCN(e)+' +'+(e.enhance||0):'未装备'}</div>
        ${e?pwBadge(e):''}</div>`;
    }).join('');
    const rows=eq.map(e=>{
      const q=Math.max(0,Math.min(4,e.quality||0));
      return `<div class="eq-row" style="border-left-color:${QCOLOR[q]}">
        <span class="eq-ico">${slotIco[e.slot]||ICON.crystal(20)}</span>
        <div style="flex:1;min-width:0">
          <div class="eq-name" style="color:${QCOLOR[q]}">${eqCN(e)}<em>${QUALITY[q]}</em>${wcTag(e)}</div>
          <div class="eq-sub">${slotNames[e.slot]||e.slot} · 强化 +${e.enhance||0} ${e.equipped?'· <b style="color:#7fe0a0">已穿戴</b>':'· 仓库中'} ${pwBadge(e)||''}</div>
        </div>
        <button class="eq-btn eq-toggle" data-eqid="${e.id}" data-slot="${e.slot}">${e.equipped?'卸下':'穿戴'}</button>
      </div>`;
    }).join('');
    html+=`
      <div class="sec-sub">穿戴/卸下在此操作；<b>强化与升品已移至 家园 › 仓库 › 强化工坊</b>（显示当前/强化后数值、品质进阶与消耗）</div>
      <div class="equip-grid">${cells}</div>
      <div class="sec-title">装备列表</div>${rows||'<div class="sec-sub">暂无装备</div>'}
      <div class="sec-title">当前属性</div>${attrRows()}`;
  } else if(roleTab==='skill'){
    // 真 BD 配置：本地暂存槽位，提交服务端权威校验（职业池/解锁/4槽）
    // 交付包 §4.3：技能库显示 = unlocked[] ∩ (当前 classId 的 initial ∪ advanced)，不显示跨职业技能
    const all=CFG.skills.skills;
    const clsId=c.classId;
    const cp=(CFG.skills.classes&&CFG.skills.classes[clsId])||{name:'',initial:[],advanced:[]};
    const poolIds=[...(cp.initial||[]),...(cp.advanced||[])];
    if(!window._bdDraft) window._bdDraft=(state.skills?.slots||[]).filter(id=>poolIds.indexOf(id)>=0);
    const equipped=window._bdDraft;
    const unlocked=(state.skills?.unlocked||[]).filter(id=>poolIds.indexOf(id)>=0);
    const visible=all.filter(s=>poolIds.indexOf(s.id)>=0);
    const color=['sk-fire','sk-ice','sk-heal','sk-shield'];
    const kindDesc=s=>s.kind==='heal'?'恢复生命':s.kind==='shield'?'吸收伤害':s.kind==='buff'?'增益':s.kind==='debuff'?'减益':s.kind==='summon'?'召唤协战':'单体伤害';
    const kindIco={physical:'sword',magic:'bolt',heal:'flask',shield:'shield',buff:'star',debuff:'skull',summon:'skull'};
    // v1.9.82 技能卡数值行：召唤显示持续回合与每跳倍率，吸血在威力后标注
    const powerText=s=>s.kind==='summon'
      ? `召唤 ${s.summonRounds||6} 回合 · 每回合 ${Number(s.summonPower||0).toFixed(1)}×`
      : `威力 ${Number(s.power||1).toFixed(1)}${s.lifesteal?' · 吸血 '+Math.round(s.lifesteal*100)+'%':''}`;
    // v1.9.56 技能升级：每级 +8%，消耗钻石随等级递增（数值读 CFG.economy.skillUpgrade）
    const SU=(CFG&&CFG.economy&&CFG.economy.skillUpgrade)||{};
    const suPer=Number(SU.perLevel||0.08), suMax=Number(SU.maxLevel||20),
          suBase=Number(SU.baseGem||60), suStep=Number(SU.stepGem||30);
    const suLv=id=>{const v=(state.skills&&state.skills.levels&&state.skills.levels[id])||1;return Math.max(1,Math.min(suMax,v));};
    const suCost=lv=>lv>=suMax?0:suBase+suStep*(lv-1);
    const upRows=visible.filter(s=>unlocked.includes(s.id)).map(s=>{
      const lv=suLv(s.id), cost=suCost(lv), mx=lv>=suMax, mult=1+suPer*(lv-1);
      const afford=((state.core&&state.core.gem)||0)>=cost;
      return `<div class="skup-row">
        <div class="sku-main"><span class="sku-name">${s.name}</span><span class="sku-lv">Lv.${lv}</span></div>
        <div class="sku-eff">效果 ×${mult.toFixed(2)} · 每级 +${(suPer*100).toFixed(0)}%${mx?' · 已满级':''}</div>
        <button class="sku-btn" data-up="${s.id}" ${(mx||!afford)?'disabled':''}>${mx?'满级':'升级 '+fmt(cost)+' 钻'}</button>
      </div>`;
    }).join('');
    const cards=equipped.map((sid,i)=>{
      const s=all.find(x=>x.id===sid)||{name:sid,kind:'physical'};
      const ic=kindIco[s.kind]||'sword';
      return `<div class="skill-card ${color[i%4]}" data-slot="${i}" style="cursor:pointer">
        <div class="sk-ico">${ICON[ic]?ICON[ic](24,'#fff3c4'):''}</div>
        <div style="flex:1;min-width:0"><div class="sn">${s.name}</div>
          <div class="sd">${kindDesc(s)} · ${powerText(s)} · CD ${s.cd||2}s</div></div>
        <div class="srm" style="font-size:10px;color:#ffd590">点击卸下</div>
      </div>`;
    }).join('');
    const lib=visible.filter(s=>!equipped.includes(s.id)).map(s=>{
      const lock=!unlocked.includes(s.id);
      const ic=kindIco[s.kind]||'sword';
      const adv=s.tier==='advanced';
      return `<div class="lib-item ${lock?'locked':''}" data-skill="${s.id}" style="cursor:${lock?'not-allowed':'pointer'};opacity:${lock?'.5':'1'}">
        <span class="li-ico">${ICON[ic]?ICON[ic](18,lock?'#7d8899':'#f0d590'):''}</span>
        <span class="li-name">${s.name}${adv?'<i style="font-style:normal;font-size:9px;color:#b98cf0;margin-left:4px">进阶</i>':''}</span>
        <span class="li-cd">CD ${s.cd||2}s</span>
        ${lock?ICON.lock(12,'#9aa7bd'):''}
      </div>`;
    }).join('');
    html+=`
      <div class="sec-sub">当前职业 <b style="color:#f0d590">${cp.name||c.classId}</b> 的技能池：${visible.length} 个（进阶技能转职后自动解锁）。点击技能库装配，点击槽位卸下，最多 4 槽。</div>
      <div class="skill-grid2">${cards}</div>
      <div class="sec-title">技能库（仅本职业）</div>${lib?`<div class="lib-grid">${lib}</div>`:`<div class="sec-sub">本职业 ${visible.length} 个技能已全部装配，无更多可选项。</div>`}
      <button class="big-btn goldbtn" id="submitBD" style="margin-top:12px">保存 BD 配置</button>
      <div class="sec-sub" style="margin-top:8px">已解锁 ${unlocked.length}/${visible.length} · 未解锁技能点击花费 300 钻石解锁</div>
      <div class="sec-title" style="margin-top:16px">技能升级（每级效果 +${(suPer*100).toFixed(0)}%）</div>
      <div class="sec-sub" style="margin-bottom:6px">花钻石升级，伤害 / 治疗 / 减伤同步提升；等级越高，下一级越贵（上限 ${suMax} 级）。</div>
      <div class="skup-list">${upRows||'<div class="sec-sub">暂无可升级技能（先解锁本职业技能）</div>'}</div>`;
  } else if(roleTab==='class'){
    const stage=(state.world&&state.world.unlocked)?state.world.unlocked.length:1;
    const cps=CFG.skills.classes||{};
    // v1.9.26 转职界面仅显示可转职职业：
    //  基础职业 → 另一基础职业（互转）+ 本系进阶职业（按等级/世界数锁定）
    //  进阶职业 → 其基础职业（回退）+ 同系进阶职业（按等级/世界数锁定）
    const curCls0=cps[c.classId]||{};
    const curTier0=curCls0.tier||0;
    const allowed=CFG.classes.classes.filter(cl=>{
      if(cl.id===c.classId) return true;                       // 当前职业保留展示（标记"当前"）
      const cp=cps[cl.id]||{}; const tier=cp.tier||0;
      if(tier===0) return curTier0===0 || curCls0.from===cl.id; // 基础互转 / 进阶回退
      return cp.from===c.classId || (curTier0===1 && cp.from===curCls0.from); // 本系进阶 / 同系进阶
    });
    const cards=allowed.map(cl=>{
      const sel=cl.id===c.classId;
      const cp=cps[cl.id]||{};
      const tier=cp.tier||0;
      const needLv=cp.needLevel||44, needStage=cp.needStage||2;
      const lockLv=tier>0&&c.level<needLv;
      const lockStage=tier>0&&stage<needStage;
      const lock=lockLv||lockStage;
      const col={'warrior':'#b8862f','mage':'#7C6BB8','fighter':'#c05252','knight':'#3f7fb8','sage':'#3f9d6d','warlock':'#8a4a9c'}[cl.id]||'#5a6b80';
      const curCls=cps[c.classId]||{};
      const isCur=sel;
      const canRevert=tier===0&&curCls.tier===1&&curCls.from===cl.id;   // v1.9.18 进阶职业可退回其基础职业
      const canSwap=tier===0&&curCls.tier===0&&!sel;                    // v1.9.26 基础职业互转
      const badge=isCur?'<span class="cl-req ok">当前</span>'
        :tier>0?`<span class="cl-req ${lock?'no':'ok'}">${lock?(lockLv?'需 Lv.'+needLv:'需第 '+needStage+' 世界'):'可进阶'}</span>`
        :canRevert?'<span class="cl-req ok">可回退</span>'
        :canSwap?'<span class="cl-req ok">可互转</span>'
        :'<span class="cl-req ok">基础职业</span>';
      const adv=(cp.advanced||[]).length?`<div class="cd">进阶技能：${cp.advanced.map(id=>(CFG.skills.skills.find(x=>x.id===id)||{}).name||id).join(' / ')}</div>`:'';
      return `<div class="class-card ${sel?'sel':''} ${lock?'locked':''}" style="background:linear-gradient(150deg,${col},rgba(0,0,0,.4))" data-cls="${cl.id}" data-lock="${lock?1:0}">
        <div><div class="cn">${cl.name}${badge}</div><div class="cd">${cl.desc||''}</div>${adv}</div><span class="chk">${sel?'✓':''}</span></div>`;
    }).join('');
    html+=`
      <div class="sec-sub">当前：${cdef?cdef.name:c.classId} Lv.${c.level} ｜ 已开启 ${stage} 个世界 ｜ 基础职业可<b style="color:#f0d590">互相转换</b>（零成本）；Lv.44 且开启第 2 世界后可进阶；进阶职业可随时<b style="color:#f0d590">退回基础职业</b>。装备属性转职时自动换算，家园外观与脸型保持不变</div>
      <div class="class-grid">${cards}</div>
      <button class="big-btn" id="doTransfer" style="margin-top:10px">确认转职 / 进阶 / 回退</button>`;
  }
  body.innerHTML=html;
  body.querySelectorAll('[data-rtab]').forEach(el=>el.onclick=()=>{roleTab=el.dataset.rtab;window._bdDraft=null;window.SFX&&SFX.tab();renderRole();});
  if(roleTab==='skill'){
    // 装配：点技能库
    body.querySelectorAll('.lib-item[data-skill]').forEach(el=>el.onclick=async()=>{
      const sid=el.dataset.skill;
      const unlocked=state.skills?.unlocked||[];
      if(!unlocked.includes(sid)){
        try{
          const r=await api('/api/player/action',{userId:uid(),action:'unlock_skill',skillId:sid});
          if(r.state){state=r.state;syncHud();}
          window.SFX&&SFX.rune();
          toastModal('技能解锁',CFG.skills.skills.find(s=>s.id===sid)?.name+' 已解锁（-300 钻石）');
          renderRole();
        }catch(e){window.SFX&&SFX.fail();toastModal('解锁失败',e.message);}
        return;
      }
      if(window._bdDraft.length>=4){toastModal('槽位已满','先卸下一个技能再装配');return;}
      window._bdDraft.push(sid);window.SFX&&SFX.rune();renderRole();
    });
    // 卸下：点槽位
    body.querySelectorAll('.skill-card[data-slot]').forEach(el=>el.onclick=()=>{
      window._bdDraft.splice(parseInt(el.dataset.slot),1);window.SFX&&SFX.unequip();renderRole();
    });
    // 提交 BD
    const sb=$('submitBD');
    if(sb)sb.onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'submit_bd',slots:window._bdDraft});
        if(r.state){state=r.state;await applyStats(r.stats);}
        window._bdDraft=null;
        window.SFX&&SFX.config();
        toastModal('BD 已保存',(r.slots||[]).length+' 个技能已生效');
        renderRole();
      }catch(e){window.SFX&&SFX.fail();toastModal('保存失败',e.message);}
    };
    // v1.9.56 技能升级：服务端权威扣钻石，返回新等级与效果倍率
    body.querySelectorAll('.sku-btn[data-up]').forEach(b=>b.onclick=async()=>{
      if(b.disabled) return;
      const sid=b.dataset.up;
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'skill_upgrade',skillId:sid});
        if(r.state){state=r.state;await refreshStats();syncHud();}
        window.SFX&&SFX.rune();
        toastModal('技能升级',`${r.skillName||sid} → Lv.${r.level} · 效果 ×${Number(r.mult||1).toFixed(2)}（消耗 ${fmt(r.cost)} 钻石）`);
        renderRole();
      }catch(e){window.SFX&&SFX.fail();toastModal('升级失败',(e&&e.message)||String(e));}
    });
  }
  if(roleTab==='equip'){
    // 穿戴/卸下（v1.9.21 强化入口已移除，统一走 家园›仓库›强化工坊）
    body.querySelectorAll('.eq-toggle').forEach(b=>b.onclick=async()=>{
      const e=(state.equip||[]).find(x=>x.id===b.dataset.eqid);
      const act=e&&e.equipped?'unequip':'equip';
      try{
        const r=await api('/api/player/action',{userId:uid(),action:act,itemId:b.dataset.eqid});
        if(r.state){state=r.state;await applyStats(r.stats);}
        window.SFX&&(act==='equip'?SFX.equip():SFX.unequip());
        toastModal(act==='equip'?'已穿戴':'已卸下',eqCN(e));
        if(window.Quest&&state.equip)Quest.set('equip',state.equip.filter(x=>x.equipped).length);
        renderRole();
      }catch(err){window.SFX&&SFX.fail();toastModal('操作失败',err.message);}
    });
  }
  if(roleTab==='class'){
    let pick=c.classId;
    body.querySelectorAll('.class-card').forEach(el=>el.onclick=()=>{
      body.querySelectorAll('.class-card').forEach(x=>{x.classList.remove('sel');x.querySelector('.chk').textContent='';});
      el.classList.add('sel');el.querySelector('.chk').textContent='✓';pick=el.dataset.cls;
    });
    $('doTransfer').onclick=async()=>{
      // 与「当前职业」比较，而不是与选中卡片比较——pick 就是被点中的那张卡，
      // 拿卡和 pick 比永远相等，会把所有转职都挡成"无需转职"。
      if(pick===c.classId){ toastModal('无需转职','当前已是'+((CFG.classes.classes.find(x=>x.id===pick)||{}).name||pick)); return; }
      const tgt=body.querySelector('.class-card[data-cls="'+pick+'"]');
      if(tgt&&tgt.dataset.lock==='1'){ window.SFX&&SFX.deny(); toastModal('条件不足','进阶需 Lv.44 且开启第 2 世界（当前 Lv.'+c.level+' · '+((state.world&&state.world.unlocked)||[]).length+' 个世界）'); return; }
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'class_advance',to:pick});
        if(r.state){state=r.state;await applyStats(r.stats);}
        applyHeroLook();                       // 探索/战斗立绘随职业切换
        if(window.Quest&&state.equip)Quest.set('equip',state.equip.filter(x=>x.equipped).length);
        window.SFX&&SFX.transfer();
        toastModal('转职成功',(r.message||'装备属性已按新职业换算')+'（家园外观与脸型保持不变）');
        roleTab='class';window._bdDraft=null;renderRole();
      }catch(e){window.SFX&&SFX.fail();toastModal('转职失败',e.message);}
    };
  }
}
function renderWarehouse(){
  const body=$('panelBody');
  const inHome=homeSub==='warehouse';
  const backHome=inHome?'<button class="sub-back" id="backToHome">‹ 返回家园</button>':'';
  // v1.9.21 强化工坊子层：所有装备 → 点选入强化栏 → 当前数值/强化后数值/真实消耗
  if(forgeSub){
    const eqAll=state&&state.equip?state.equip:[];
    const whAll=state&&state.warehouse?state.warehouse:[];
    const whEqAll=whAll.filter(it=>it.kind==='equip');
    const enhCfg=(CFG&&CFG.equipment&&CFG.equipment.enhance)||{};
    const gain=enhCfg.statGainPerLevel!=null?enhCfg.statGainPerLevel:0.08;
    const goldBase=enhCfg.costBase||1200, growth=enhCfg.costGrowth||1.35;
    const c=state.core||{};
    const sel=forgeSel?eqAll.find(x=>x.id===forgeSel):null;
    const KNAME={atk:'攻击',def:'防御',hp:'生命',crit:'暴击',speed:'速度'};
    const fmtStat=(k,v)=>k==='hp'||k==='speed'?String(Math.round(v)):(k==='crit'?(v*100).toFixed(1)+'%':(Math.round(v*10)/10).toFixed(1));
    const slotIcoF={weapon:ICON.sword(22,'#f0d590'),helmet:ICON.helmet(22,'#f0d590'),armor:ICON.armor(22,'#f0d590'),trinket:ICON.ring(22,'#f0d590')};
    const cell=(id,slot,name,lv,badge,cls,dis,dataset)=>`
      <div class="forge-cell${cls||''}${dis?' dis':''}" ${dataset}>
        <span class="fc-ico">${slotIcoF[slot]||ICON.crystal(22,'#f0d590')}</span>
        <span class="fc-name">${name}</span>
        <span class="fc-lv">+${lv||0}</span>
        ${badge?`<em class="fc-badge">${badge}</em>`:''}
      </div>`;
    // v1.9.50 工坊装备分组：身上装备（穿戴中，可强化/升品）→ 背包装备 → 仓库装备（需取出）
    const wornEq=eqAll.filter(e=>e.equipped), bagOnlyEq=eqAll.filter(e=>!e.equipped);
    const grpWorn=wornEq.length?`<div class="forge-grp-t worn">身上装备（穿戴中 · 可直接强化/升品）</div>`+wornEq.map(e=>cell(e.id,e.slot,eqCN(e),e.enhance,'穿戴中',
      forgeSel===e.id?' sel':'',false,`data-fid="${e.id}"`)).join(''):'';
    const grpBag=bagOnlyEq.length?`<div class="forge-grp-t">背包装备</div>`+bagOnlyEq.map(e=>cell(e.id,e.slot,eqCN(e),e.enhance,'背包',
      forgeSel===e.id?' sel':'',false,`data-fid="${e.id}"`)).join(''):'';
    const grpWh=whEqAll.length?`<div class="forge-grp-t">仓库装备（需先取出）</div>`+whEqAll.map(it=>cell(it.id,(it.equip&&it.equip.slot)||'',eqCN(it.equip||{}),(it.equip&&it.equip.enhance)||0,'仓库中','',
      true,`data-wh="${it.id}"`)).join(''):'';
    let listHtml=grpWorn+grpBag+grpWh;
    if(!eqAll.length&&!whEqAll.length)listHtml='<div class="sec-sub">暂无任何装备</div>';
    // 强化栏
    let forgeBar;
    if(!sel){
      forgeBar=`<div class="forge-empty">点击左侧装备，放入强化栏</div>`;
    }else{
      const lv=sel.enhance||0;
      const nextCost=Math.round(goldBase*Math.pow(growth,lv));
      const canPay=c.gold>=nextCost&&(c.material||0)>=2;
      const PWT={atk:3,def:2,hp:0.4,crit:800,speed:0};
      let curPw=0,nextPw=0;
      let props=Object.keys(sel.stats||{}).map(k=>{
        const base=sel.stats[k]||0;
        const cur=base*(1+gain*lv), next=base*(1+gain*(lv+1));
        const w=PWT[k]||0; curPw+=cur*w; nextPw+=next*w;
        return `<div class="fp-row"><span class="fp-k">${KNAME[k]||k}</span>
          <span class="fp-cur">${fmtStat(k,cur)}</span><span class="fp-arrow">→</span>
          <span class="fp-next">${fmtStat(k,next)}</span></div>`;
      }).join('')||'<div class="fp-row"><span class="fp-k">该装备无属性</span></div>';
      props+=`<div class="fp-row"><span class="fp-k">战力</span>
        <span class="fp-cur">${Math.round(curPw)}</span><span class="fp-arrow">→</span>
        <span class="fp-next" style="color:#f0d590">+${Math.round(nextPw)}</span></div>`;
      forgeBar=`
        <div class="forge-slot">
          <div class="fs-head">${slotIcoF[sel.slot]||ICON.crystal(26,'#f0d590')}
            <b>${eqCN(sel)}</b>${pwBadge(sel)}<em class="fc-badge">${sel.equipped?'穿戴中':'背包'}</em><span class="fs-lv">+${lv} → +${lv+1}</span></div>
          <div class="sec-title">数值对比（每级装备属性 +${Math.round(gain*100)}%）</div>
          <div class="fp-props">${props}</div>
          <div class="forge-cost">
            <div class="fco-row"><span>消耗</span><b style="color:#f0d590">${ICON.gold(14)} ${fmt(nextCost)} 金币</b><b style="color:#5fd38a">${ICON.crystal(14,'#5fd38a')} 2 强化材料</b></div>
            <div class="fco-row sub"><span>持有</span><b>${fmt(c.gold||0)} 金币</b><b>${fmt(c.material||0)} 强化材料</b></div>
          </div>
          <button class="big-btn goldbtn" id="doForge" ${canPay?'':'disabled style="opacity:.45;cursor:not-allowed"'}>${canPay?'强 化':'资源不足'}</button>
          <div class="strength-note">强化必成功 · 战力即时更新</div>
        </div>`;
    }
    // v1.9.44 升品栏：装备升品质入口自仓库移入工坊（品质+1 最高混沌，属性按品质乘数重算）
    let forgeQuBar='';
    if(!sel){
      forgeQuBar=`<div class="forge-empty">先在左侧选择装备，再进行升品</div>`;
    }else{
      const dec=(CFG&&CFG.economy&&CFG.economy.drop&&CFG.economy.drop.decompose)||{};
      const GB=dec.goldBase||[40,120,300,800,2000,4200,8000,15000];
      const MB=dec.matBase||[10,30,80,200,500,1200,2600,6000];
      const qu=(CFG&&CFG.economy&&CFG.economy.drop&&CFG.economy.drop.qualityUp)||{};
      const gM=qu.goldMult!=null?qu.goldMult:1.2, mM=qu.matMult!=null?qu.matMult:0.6;
      const qArr=(CFG&&CFG.equipment&&CFG.equipment.quality)||[];
      const sqi=Math.max(0,Math.min(7,sel.quality||0));
      if(sqi>=7){
        forgeQuBar=`
          <div class="sec-title">升品栏（品质+1 · 属性按品质乘数重算）</div>
          <div class="forge-slot"><div class="fs-head"><b style="color:${QCOLOR[sqi]}">${QUALITY[sqi]}</b>
            <em class="fc-badge">已达最高品质（混沌），无法继续升品</em></div></div>`;
      }else{
        const nq=sqi+1;
        const qGold=Math.round((GB[Math.min(nq,7)]||40)*gM), qMat=Math.round((MB[Math.min(nq,7)]||10)*mM);
        const qCan=c.gold>=qGold&&(c.material||0)>=qMat;
        const mOld=(qArr[sqi]&&qArr[sqi].mult)||1, mNew=(qArr[nq]&&qArr[nq].mult)||1;
        const ratio=mOld>0?mNew/mOld:1.25;
        forgeQuBar=`
          <div class="sec-title">升品栏（品质+1 · 属性按品质乘数重算）</div>
          <div class="forge-slot">
            <div class="fs-head">${slotIcoF[sel.slot]||ICON.crystal(26,'#f0d590')}
              <b>${eqCN(sel)}</b><em class="fc-badge">${sel.equipped?'穿戴中':'背包'}</em>
              <span class="fs-lv"><b style="color:${QCOLOR[sqi]}">${QUALITY[sqi]}</b> → <b style="color:${QCOLOR[nq]}">${QUALITY[nq]}</b></span></div>
            <div class="sec-sub">全部属性 × ${ratio.toFixed(2)}（品质乘数 ${mOld} → ${mNew}）· 强化等级保持不变</div>
            <div class="forge-cost">
              <div class="fco-row"><span>消耗</span><b style="color:#f0d590">${ICON.gold(14)} ${fmt(qGold)} 金币</b><b style="color:#5fd38a">${ICON.crystal(14,'#5fd38a')} ${qMat} 强化材料</b></div>
              <div class="fco-row sub"><span>持有</span><b>${fmt(c.gold||0)} 金币</b><b>${fmt(c.material||0)} 强化材料</b></div>
            </div>
            <button class="big-btn goldbtn" id="doQuBtn" ${qCan?'':'disabled style="opacity:.45;cursor:not-allowed"'}>${qCan?'升 品':'资源不足'}</button>
            <div class="strength-note">升品不可逆 · 战力即时更新</div>
          </div>`;
      }
    }
    body.innerHTML=`
      ${backHome}
      <button class="sub-back" id="forgeBack">‹ 返回仓库</button>
      <div class="sec-title">⚒ 强化工坊</div>
      <div class="sec-sub">身上装备 / 背包装备均可直接强化与升品 · 消耗随等级递增（${enhCfg.costBase||1200} × ${(enhCfg.costGrowth||1.35)}^等级 金币 + 1 材料）· 仓库装备需先取出</div>
      <div class="forge-grid">${listHtml}</div>
      <div class="sec-title">强化栏</div>
      ${forgeBar}
      ${forgeQuBar}`;
    $('forgeBack').onclick=()=>{forgeSub=false;forgeSel=null;renderWarehouse();};
    if(inHome)$('backToHome').onclick=()=>{forgeSub=false;forgeSel=null;homeSub=null;renderHome();};
    body.querySelectorAll('.forge-cell[data-fid]').forEach(el=>el.onclick=()=>{
      forgeSel=el.dataset.fid;window.SFX&&SFX.tab();renderWarehouse();
    });
    body.querySelectorAll('.forge-cell[data-wh]').forEach(el=>el.onclick=()=>{
      toastModal('仓库中的装备无法直接强化/升品','请先在仓库取出到背包后再强化/升品');
    });
    const doBtn=$('doForge');
    if(doBtn)doBtn.onclick=async()=>{
      try{
        const r=await api('/api/player/action',{userId:uid(),action:'enhance',itemId:sel.id});
        if(r.state){state=r.state;await applyStats(r.stats);}
        window.SFX&&SFX.forge();
        const cost=r.enhanceCost||{};
        toastModal('强化成功',eqCN(sel)+' 强化至 +'+(r.enhanceLevel||'?')+
          (cost.gold?('（消耗 '+fmt(cost.gold)+' 金币 / '+fmt(cost.material||0)+' 强化材料）'):''));
        if(window.Quest)Quest.progress('enhance',1);
        renderWarehouse();
      }catch(e){window.SFX&&SFX.fail();toastModal('强化失败',e.message);}
    };
    // v1.9.44 升品按钮：确认弹窗 → quality_up（服务端权威）
    const quBtn=$('doQuBtn');
    if(quBtn)quBtn.onclick=()=>{
      const dec=(CFG&&CFG.economy&&CFG.economy.drop&&CFG.economy.drop.decompose)||{};
      const GB=dec.goldBase||[40,120,300,800,2000,4200,8000,15000];
      const MB=dec.matBase||[10,30,80,200,500,1200,2600,6000];
      const qu=(CFG&&CFG.economy&&CFG.economy.drop&&CFG.economy.drop.qualityUp)||{};
      const gM=qu.goldMult!=null?qu.goldMult:1.2, mM=qu.matMult!=null?qu.matMult:0.6;
      const sqi=Math.max(0,Math.min(7,sel.quality||0)), nq=sqi+1;
      const qGold=Math.round((GB[Math.min(nq,7)]||40)*gM), qMat=Math.round((MB[Math.min(nq,7)]||10)*mM);
      showModal(`<h2>装备升品</h2><div class="msub">将 <b style="color:${QCOLOR[sqi]}">${QUALITY[sqi]}</b>「${eqCN(sel)}${sel.enhance?(' +'+sel.enhance):''}」提升为 <b style="color:${QCOLOR[nq]}">${QUALITY[nq]}</b>？<br>属性按品质乘数重算提升 · 消耗 <b style="color:#ffd76b">${fmt(qGold)} 金币</b> + <b style="color:#9ad6ff">${qMat} 强化材料</b><br><span style="color:#e07b7b">升品不可逆，无法找回原品质</span></div>
        <div class="divider"></div>
        <div class="enc-btns"><button class="big-btn goldbtn" id="quConfirm">确认升品</button><button class="btn-gray" id="quCancel">再想想</button></div>`);
      $('quCancel').onclick=hideModal;
      $('quConfirm').onclick=async()=>{
        try{
          const r=await api('/api/player/action',{userId:uid(),action:'quality_up',itemId:sel.id});
          hideModal();
          if(r.state){state=r.state;await applyStats(r.stats);}
          window.SFX&&SFX.buy&&SFX.buy();
          toastModal('升品成功',eqCN(sel)+' → '+QUALITY[Math.min(r.quality||nq,7)]+'（消耗 '+fmt(r.cost&&r.cost.gold||qGold)+' 金币 / '+(r.cost&&r.cost.material||qMat)+' 强化材料）');
          forgeSel=null;
          renderWarehouse();
        }catch(err){ hideModal(); window.SFX&&SFX.fail(); toastModal('升品失败',err.message); }
      };
    };
    return;
  }
  // 装备详情子层（仅穿戴/卸下；强化统一走强化工坊）
  if(equipDetailSlot){
    const eq=state&&state.equip?state.equip:[];
    const eObj=eq.find(x=>x.slot===equipDetailSlot)||eq.find(x=>x.id===equipDetailSlot);
    if(!eObj){equipDetailSlot=null;}
    else{
      const isEquipped=!!eObj.equipped;
      const meta={weapon:['武器',ICON.sword(34)],helmet:['头盔',ICON.helmet(34)],armor:['护甲',ICON.armor(34)],trinket:['饰品',ICON.ring(34)]}[eObj.slot]||['装备',ICON.crystal(34)];
      body.innerHTML=`
        ${backHome}
        <div class="equip-detail">
          <div class="ed-head">
            <div class="ed-ico">${meta[1]}</div>
            <div><div class="ed-name">${eqCN(eObj)}</div><div class="ed-meta">部位：${meta[0]} ｜ 强化等级 +${eObj.enhance||0} ｜ ${isEquipped?'穿戴中':'背包中'}</div><div class="ed-pw">${pwBadge(eObj)||'<span class="eq-pw">战力计算中…</span>'}</div></div>
            <button class="ed-back" id="edBack">‹ 返回仓库</button>
          </div>
          <div class="sec-title">装备属性</div>
          ${attrRows()}
          <div class="enhance-bar">
            <button class="big-btn" id="edToggle">${isEquipped?'卸下装备':'穿戴装备'}</button>
            <button class="big-btn goldbtn" id="edToForge" style="margin-left:8px">去强化工坊（强化/升品） ›</button>
          </div>
        </div>`;
      $('edBack').onclick=()=>{equipDetailSlot=null;renderWarehouse();};
      if(inHome)$('backToHome').onclick=()=>{equipDetailSlot=null;homeSub=null;renderHome();};
      $('edToggle').onclick=async()=>{
        try{
          const r=await api('/api/player/action',{userId:uid(),action:isEquipped?'unequip':'equip',itemId:eObj.id});
          if(r.state){state=r.state;await applyStats(r.stats);}
          window.SFX&&(isEquipped?SFX.unequip():SFX.equip());
          toastModal(isEquipped?'已卸下':'已穿戴',eqCN(eObj));renderWarehouse();
        }catch(err){window.SFX&&SFX.fail();toastModal('操作失败',err.message);}
      };
      $('edToForge').onclick=()=>{clearWhBatch();forgeSub=true;forgeSel=eObj.id;window.SFX&&SFX.tab();renderWarehouse();};
      return;
    }
  }
  // 仓库交互（v1.9.32 重构）：装备点击直存直取（无弹窗）；可堆叠物品点击→数量确认条（取消/确定）；
  // 一键批量存入/取出；成功反馈用面板内轻量浮条（2 秒自动消失），不再弹 toastModal
  const wh=state?.warehouse||[];
  const eqAll=state?.equip||[];
  const bagEq=eqAll.filter(e=>!e.equipped);
  const whEq=wh.filter(it=>it.kind==='equip');
  const whGoods=wh.filter(it=>it.kind!=='equip');
  const bagGoods=Object.entries((state&&state.ext&&state.ext.bag)||{}).filter(([k,v])=>(v|0)>0);
  const slotIco={weapon:ICON.sword(24,'#2a1c08'),helmet:ICON.helmet(24,'#2a1c08'),armor:ICON.armor(24,'#2a1c08'),trinket:ICON.ring(24,'#2a1c08')};
  // v1.9.50 批量选择模式：whBatch='in'|'out'|'dec'；whBatchSel 存 'eq:<id>'/'mat:<id>'
  const batchOn=!!whBatch;
  const isSel=(kind,id)=>whBatchSel.has(kind+':'+id);
  const selCls=(kind,id)=>isSel(kind,id)?' wh-sel':'';
  const batchLabel=whBatch==='in'?'存入':whBatch==='out'?'取出':'分解';
  const batchBar=batchOn?`
    <div class="wh-batchbar" id="whBatchBar">
      <div class="wbb-line"><b>批量${batchLabel} · 选择模式</b><span>点击物品勾选，再次点击取消勾选</span></div>
      <div class="wbb-line wbb-actions"><span class="wbb-count">已选 <b id="wbbCount">${whBatchSel.size}</b> 件</span><button class="wbb-run" id="wbbRun">确认${batchLabel}</button><button class="wbb-cancel" id="wbbCancel">退出选择</button></div>
    </div>`:'';
  body.innerHTML=`
    ${backHome}
    <button class="big-btn goldbtn" id="openForge" style="width:100%">⚒ 强化工坊 · 强化 / 升品装备（数值对比 · 品质进阶 · 消耗）</button>
    ${batchBar}
    <div class="wh-confirm hidden" id="whConfirm">
      <span class="wh-c-name" id="whCName"></span>
      <input id="whQtyInput" type="number" min="1" value="1">
      <button class="wh-mini btn-gray" id="whAll">全部</button>
      <button class="wh-mini btn-gray" id="whNo">取消</button>
      <button class="wh-mini wh-ok" id="whYes">确定</button>
    </div>
    <div class="sec-title">背包 · 装备 <span class="sec-sub" style="display:inline">（未穿戴 ${bagEq.length} 件 · ${batchOn?(whBatch==='in'?'点击勾选存入':'当前不可选'):'点击装备直接存入'}）</span><button class="wh-batch ${whBatch==='in'?'on':''}" id="batchIn">⇪ 批量存入</button></div>
    <div class="wg-grid">${bagEq.map(e=>`
      <div class="wg-cell equip${batchOn&&whBatch==='in'?selCls('eq',e.id):''}" data-in="${e.id}" data-selkey="eq:${e.id}">
        <div class="wgi" style="background:#b8862f">${slotIco[e.slot]||ICON.sword(24,'#2a1c08')}</div>${eqCN(e)} +${e.enhance||0}<div class="wq">x1</div>
        ${wcTag(e)||''}
        ${pwBadge(e)||''}
        <span class="wg-detail" data-detail="${e.slot}">详情</span>
      </div>`).join('')||'<div class="sec-sub">背包暂无未穿戴装备</div>'}</div>
    <div class="sec-title">背包 · 可回收物品 <span class="sec-sub" style="display:inline">（${batchOn&&whBatch==='in'?'点击勾选存入':'点击选择数量存入'}）</span></div>
    <div class="wg-grid">${bagGoods.map(([id,n])=>`
      <div class="wg-cell${batchOn&&whBatch==='in'?selCls('mat',id):''}" data-gin="${id}" data-n="${n|0}" data-selkey="mat:${id}"><div class="wgi" style="background:#8892a4">${ICON.crystal(20,'#2a1c08')}</div>${itemCN(id)}<div class="wq">x${n|0}</div></div>`).join('')||'<div class="sec-sub">背包暂无材料</div>'}</div>
    <div class="sec-title">仓库 · 装备 <span class="sec-sub" style="display:inline">（${whEq.length} 件 · ${batchOn?(whBatch==='out'||whBatch==='dec'?'点击勾选':'当前不可选'):'点击装备取出 · 点 ♻ 分解换金币/材料'}）</span><button class="wh-batch ${whBatch==='dec'?'on':''}" id="batchDec" ${whEq.length?'':'disabled style="opacity:.45"'}>♻ 批量分解</button><button class="wh-batch ${whBatch==='out'?'on':''}" id="batchOut">⇩ 批量取出</button></div>
    <div class="wg-grid">${whEq.map(it=>{
      const e=it.equip||{};
      const DEC=[40,120,300,800,2000], MAT=[10,30,80,200,500], qi=Math.max(0,Math.min(4,e.quality||0));
      return `<div class="wg-cell equip${batchOn&&(whBatch==='out'||whBatch==='dec')?selCls('eq',it.id):''}" data-out="${it.id}" data-selkey="eq:${it.id}">
        <div class="wgi" style="background:#b8862f">${slotIco[e.slot]||ICON.sword(24,'#2a1c08')}</div>${eqCN(e)}${e.enhance?(' +'+e.enhance):''}<div class="wq">x1</div>
        ${pwBadge(it.equip)||''}
        <span class="wg-detail dec" data-dec="${it.id}" title="分解">♻ ${Math.round((DEC[qi]||40)*(1+(e.enhance||0)*0.15))}金+${MAT[qi]||10}材</span>
      </div>`;}).join('')||'<div class="sec-sub">仓库暂无装备</div>'}</div>
    <div class="sec-title">仓库 · 可回收物品 <span class="sec-sub" style="display:inline">（已用 ${wh.length} / 200 格 · ${batchOn&&whBatch==='out'?'点击勾选取出':'点击选择数量取出'}）</span></div>
    <div class="wg-grid">${whGoods.map(it=>`
      <div class="wg-cell${batchOn&&whBatch==='out'?selCls('mat',it.id):''}" data-gout="${it.id}" data-n="${it.qty}" data-selkey="mat:${it.id}"><div class="wgi" style="background:${it.kind==='consume'?'#5fd38a':'#8892a4'}">${ICON.crystal(20,'#2a1c08')}</div>${itemCN(it.id)}<div class="wq">x${it.qty}</div></div>`).join('')||'<div class="sec-sub">空</div>'}</div>
    <div class="wh-flash hidden" id="whFlash"></div>`;
  // 轻量浮条（替代弹窗提示，2.2 秒自动消失，不阻塞操作）
  const flash=(msg)=>{ const f=$('whFlash'); if(!f)return; f.textContent=msg; f.classList.remove('hidden');
    clearTimeout(f._t); f._t=setTimeout(()=>f.classList.add('hidden'),2200); };
  // v1.9.50 批量选择：切换勾选（只改 DOM class + 计数，不整页重绘，保留勾选态）
  const pickToggle=(el,key)=>{
    if(whBatchSel.has(key)){ whBatchSel.delete(key); el.classList.remove('wh-sel'); }
    else{ whBatchSel.add(key); el.classList.add('wh-sel'); }
    const cnt=$('wbbCount'); if(cnt)cnt.textContent=whBatchSel.size;
    window.SFX&&SFX.tab&&SFX.tab();
  };
  const updateBatchCount=()=>{ const cnt=$('wbbCount'); if(cnt)cnt.textContent=whBatchSel.size; };
  const enterBatch=(mode)=>{
    // 严格检测：切换批量类型时清空旧勾选；点同一按钮则退出
    if(whBatch===mode){ clearWhBatch(); renderWarehouse(); flash('已退出批量选择'); return; }
    whBatch=mode; whBatchSel.clear(); window.SFX&&SFX.tab&&SFX.tab(); renderWarehouse();
  };
  // 单次存取（服务端权威，成功后刷新面板再浮条反馈）
  const doWh=async(action,itemId,qty,name)=>{
    try{
      const r=await api('/api/player/action',{userId:uid(),action,itemId,qty});
      if(r.state){state=r.state;syncHud();}
      window.SFX&&(action==='withdraw'||action==='wh_equip_out'?SFX.bagOut():SFX.bagIn());
      renderPanel();
      flash((action==='deposit'||action==='wh_equip_in'?'已存入 ':'已取出 ')+(name||itemCN(itemId))+(qty>1?' ×'+qty:''));
    }catch(e){window.SFX&&SFX.fail();flash('操作失败：'+e.message);}
  };
  // v1.9.44 装备升品入口已移至 家园›仓库›强化工坊（升品栏），仓库内不再提供 ⬆ 角标
  // 可堆叠物品数量确认条：显示物品名/最大量，输入或点「全部」，确定才真正存取
  let confirmOp=null; // {id, max, action, name}
  const openConfirm=(id,max,action,name)=>{
    confirmOp={id,max,action,name};
    $('whCName').textContent=name+'（持有 '+max+'）';
    const inp=$('whQtyInput'); inp.value=1; inp.max=max;
    $('whConfirm').classList.remove('hidden');
    inp.focus();
  };
  $('whAll').onclick=()=>{ if(confirmOp)$('whQtyInput').value=confirmOp.max; };
  $('whNo').onclick=()=>{ confirmOp=null; $('whConfirm').classList.add('hidden'); };
  $('whYes').onclick=async()=>{
    if(!confirmOp)return;
    const q=Math.max(1,Math.min(confirmOp.max,parseInt($('whQtyInput').value)||1));
    const op=confirmOp; confirmOp=null; $('whConfirm').classList.add('hidden');
    await doWh(op.action,op.id,q,op.name);
  };
  $('whQtyInput').onkeydown=(e)=>{ if(e.key==='Enter')$('whYes').click(); };
  // v1.9.40 分解仓库装备 / v1.9.41 加材料返还：确认弹窗 → 服务端权威结算 → 金币/材料入账
  const decEst=(e)=>{ const DEC=[40,120,300,800,2000], MAT=[10,30,80,200,500], qi=Math.max(0,Math.min(4,e.quality||0));
    return {gold:Math.round((DEC[qi]||40)*(1+(e.enhance||0)*0.15)), mat:MAT[qi]||10}; };
  body.querySelectorAll('.wg-detail.dec').forEach(el=>{
    el.onclick=(ev)=>{
      ev.stopPropagation();  // 不触发外层"取出"
      if(whBatch){ flash('批量模式下请点击装备本体勾选，或用「确认分解」执行'); return; }
      const id=el.dataset.dec;
      const it=(state.warehouse||[]).find(x=>x.id===id&&!x.hidden&&x.kind==='equip');
      const e=it&&it.equip||{};
      const est=decEst(e);
      showModal(`<h2>分解装备</h2><div class="msub">将 <b style="color:#f0d590">${eqCN(e)}${e.enhance?(' +'+e.enhance):''}</b> 分解为金币与材料？<br>预计返还 <b style="color:#ffd76b">${fmt(est.gold)} 金币</b> + <b style="color:#9ad6ff">${est.mat} 强化材料</b>（分解后装备消失，无法找回）</div>
        <div class="divider"></div>
        <div class="enc-btns"><button class="btn-red" id="doDec">确认分解</button><button class="btn-gray" id="cancelDec">再想想</button></div>`);
      $('cancelDec').onclick=hideModal;
      $('doDec').onclick=async()=>{
        try{
          const r=await api('/api/player/action',{userId:uid(),action:'storage_decompose',itemId:id});
          hideModal();
          if(r.state){state=r.state;syncHud();}
          window.SFX&&SFX.coin&&SFX.coin();
          toastModal('分解成功',eqCN(e)+(e.enhance?(' +'+e.enhance):'')+' → 金币 +'+fmt(r.goldGain||est.gold)+' · 强化材料 +'+(r.matGain||est.mat));
          renderPanel();
        }catch(err){ hideModal(); window.SFX&&SFX.fail(); toastModal('分解失败',err.message); }
      };
    };
  });
  // v1.9.50 批量按钮：进入/切换选择模式（不再一键全执行）
  const bdBtn=$('batchDec');
  if(bdBtn)bdBtn.onclick=()=>{ if(!whEq.length&&whBatch!=='dec'){flash('仓库没有装备可分解');return;} enterBatch('dec'); };
  const openForgeBtn=$('openForge');
  if(openForgeBtn)openForgeBtn.onclick=()=>{clearWhBatch();forgeSub=true;forgeSel=null;window.SFX&&SFX.tab();renderWarehouse();};
  // v1.9.50 背包装备：批量模式=勾选；否则点击直接存入
  body.querySelectorAll('.wg-cell[data-in]').forEach(el=>el.onclick=()=>{
    const id=el.dataset.in;
    if(whBatch){ if(whBatch!=='in'){flash('当前为批量'+batchLabel+'模式，背包装备不可选');return;} pickToggle(el,'eq:'+id); return; }
    doWh('wh_equip_in',id,1,eqCN((state.equip||[]).find(x=>x.id===id)||{}));
  });
  // v1.9.50 仓库装备：批量模式=勾选（取出/分解）；否则点击直接取出
  body.querySelectorAll('.wg-cell[data-out]').forEach(el=>el.onclick=()=>{
    const id=el.dataset.out;
    if(whBatch){ if(whBatch!=='out'&&whBatch!=='dec'){flash('当前为批量存入模式，仓库装备不可选');return;} pickToggle(el,'eq:'+id); return; }
    const it=wh.find(x=>x.id===id);
    doWh('wh_equip_out',id,1,eqCN((it&&it.equip)||{}));
  });
  // v1.9.50 背包材料：批量模式=勾选（存入，整份）；否则打开数量确认条
  body.querySelectorAll('.wg-cell[data-gin]').forEach(el=>el.onclick=()=>{
    if(whBatch){ if(whBatch!=='in'){flash('当前为批量'+batchLabel+'模式，背包材料不可选');return;} pickToggle(el,'mat:'+el.dataset.gin); return; }
    openConfirm(el.dataset.gin,el.dataset.n|0,'deposit',itemCN(el.dataset.gin));
  });
  // v1.9.50 仓库材料：批量模式=勾选（取出，整份）；否则打开数量确认条
  body.querySelectorAll('.wg-cell[data-gout]').forEach(el=>el.onclick=()=>{
    if(whBatch){ if(whBatch!=='out'){flash('当前为批量'+batchLabel+'模式，仓库材料不可选');return;} pickToggle(el,'mat:'+el.dataset.gout); return; }
    openConfirm(el.dataset.gout,el.dataset.n|0,'withdraw',itemCN(el.dataset.gout));
  });
  // 详情角标（穿戴/强化入口，不触发存入）
  body.querySelectorAll('.wg-detail[data-detail]').forEach(el=>el.onclick=ev=>{
    ev.stopPropagation();
    clearWhBatch();  // v1.9.50 严格检测：进入装备详情即退出批量模式
    equipDetailSlot=el.dataset.detail;
    window.SFX&&SFX.tab();
    renderWarehouse();
  });
  $('batchIn').onclick=()=>enterBatch('in');
  $('batchOut').onclick=()=>enterBatch('out');
  // v1.9.50 退出选择：严格检测（按钮/返回家园/面板切换均已清模式）
  const wbbCancel=$('wbbCancel');
  if(wbbCancel)wbbCancel.onclick=()=>{clearWhBatch();renderWarehouse();flash('已退出批量选择');};
  // v1.9.50 确认执行：只处理勾选中的物品（服务端权威，逐件调用现有单件接口）
  const wbbRun=$('wbbRun');
  if(wbbRun)wbbRun.onclick=()=>{
    if(!whBatchSel.size){flash('请先勾选要操作的物品');return;}
    const mode=whBatch;
    const keys=[...whBatchSel];
    const exec=async()=>{
      wbbRun.disabled=true;
      let eqN=0,matN=0,tg=0,tm=0,err='';
      try{
        for(const key of keys){
          const [kind,id]=key.split(':');
          if(mode==='in'){
            if(kind==='eq'){ const r=await api('/api/player/action',{userId:uid(),action:'wh_equip_in',itemId:id}); if(r.state){state=r.state;syncHud();} eqN++; }
            else{ const n=(bagGoods.find(x=>x[0]===id)||[id,1])[1]|0; const r=await api('/api/player/action',{userId:uid(),action:'deposit',itemId:id,qty:n}); if(r.state){state=r.state;syncHud();} matN++; }
          }else if(mode==='out'){
            if(kind==='eq'){ const r=await api('/api/player/action',{userId:uid(),action:'wh_equip_out',itemId:id}); if(r.state){state=r.state;syncHud();} eqN++; }
            else{ const it=whGoods.find(x=>x.id===id); const r=await api('/api/player/action',{userId:uid(),action:'withdraw',itemId:id,qty:it?it.qty:1}); if(r.state){state=r.state;syncHud();} matN++; }
          }else{ // dec：批量分解勾选项，逐件服务端权威结算
            const r=await api('/api/player/action',{userId:uid(),action:'storage_decompose',itemId:id});
            if(r.state){state=r.state;syncHud();}
            tg+=(r.goldGain||0); tm+=(r.matGain||0); eqN++;
          }
        }
      }catch(e){ err=e.message; }
      hideModal(); clearWhBatch();
      renderPanel();
      window.SFX&&(mode==='dec'?SFX.coin&&SFX.coin():SFX.bagIn&&SFX.bagIn());
      if(mode==='dec') toastModal('批量分解成功','分解 '+eqN+' 件装备 → 金币 +'+fmt(tg)+' · 强化材料 +'+tm+(err?'（部分失败：'+err+'）':''));
      else flash((mode==='in'?'批量存入完成':'批量取出完成')+'：装备 '+eqN+' 件 · 材料 '+matN+' 种'+(err?'（部分失败：'+err+'）':''));
    };
    // 分解不可逆：确认弹窗汇总收益（存入/取出直接执行）
    if(mode==='dec'){
      let tg=0,tm=0;
      keys.forEach(k=>{ const [kind,id]=k.split(':'); if(kind!=='eq')return;
        const it=wh.find(x=>x.id===id&&x.kind==='equip'); if(it){const s=decEst(it.equip||{});tg+=s.gold;tm+=s.mat;} });
      showModal(`<h2>批量分解装备</h2><div class="msub">将勾选的 <b style="color:#f0d590">${keys.length} 件装备</b> 分解？<br>预计返还 <b style="color:#ffd76b">${fmt(tg)} 金币</b> + <b style="color:#9ad6ff">${tm} 强化材料</b><br><span style="font-size:12px;color:#8a93a6">分解后装备消失，无法找回</span></div>
        <div class="divider"></div>
        <div class="enc-btns"><button class="btn-red" id="doBatchDecConfirm">确认分解</button><button class="btn-gray" id="cancelBatchDec">再想想</button></div>`);
      $('cancelBatchDec').onclick=()=>{hideModal();wbbRun.disabled=false;};
      $('doBatchDecConfirm').onclick=exec;
    }else exec();
  };
  if(inHome)$('backToHome').onclick=()=>{clearWhBatch();homeSub=null;renderHome();};
}
function renderMap(){
  const body=$('panelBody');
  const worlds=(CFG&&CFG.worlds&&CFG.worlds.worlds||[]).filter(w=>w.id!=='HOME'); // v1.9.31 家园不在世界列表（走底部家园按钮进入）
  const unlocked=state&&state.world&&state.world.unlocked||['W1'];
  const cur=worldMap.worldId;
  const bgOf={grassland:'bg_explore_grassland',forest:'bg_explore_forest',desert:'bg_explore_desert',snow:'bg_explore_snow',cave:'bg_explore_cave',arena:'bg_battle_arena'};
  body.innerHTML=`
    <div class="sec-title">世界地图 · 6 大世界</div>
    <div class="sec-sub">当前：${cur} · 主线链式推进，通道成环。点已解锁世界可直接传送（免费 · 无冷却）</div>
    ${worlds.map(w=>{
      const isCur=w.id===cur, isUnlocked=unlocked.indexOf(w.id)>=0;
      const bg=bgOf[w.theme]||'bg_explore_grassland';
      return `<div class="preview-box world-map-item ${isCur?'cur':''} ${isUnlocked?'':'locked'}" data-world="${w.id}" style="height:68px;background-image:linear-gradient(90deg,rgba(8,13,22,.94) 34%,rgba(8,13,22,.66) 72%,rgba(8,13,22,.52)),url(${RES}v3/backgrounds/${bg}.png);background-size:cover;background-position:center;cursor:${isUnlocked&&!isCur?'pointer':'default'}">
        <div class="wm-no">${w.id}</div>
        <div class="wm-txt">
          <div class="pt">${w.name}${isCur?'<span class="wm-tag cur">当前</span>':isUnlocked?'<span class="wm-tag ok">已解锁</span>':'<span class="wm-tag">未解锁</span>'}</div>
          <div class="sec-sub">${isUnlocked?'可探索 · 点击传送':'需 Lv.'+w.level+' ｜ 沿通道推进解锁'}</div>
        </div>
        ${isUnlocked?'':'<div class="wm-lock">'+ICON.lock(18,'#8fa0b8')+'</div>'}
      </div>`;
    }).join('')}
    <div class="sec-sub" style="margin-top:12px">提示：也可在大世界中找到发光的「⇄ 世界通道」直接传送。</div>
    <div class="sec-title" style="margin-top:16px">⚔ 副本试炼 <span class="sec-sub" style="display:inline">（每日次数 · 战败不扣次数）</span></div>
    <div id="dgZone"><div class="sec-sub">副本次数加载中…</div></div>`;
  body.querySelectorAll('.world-map-item').forEach(el=>{
    el.onclick=()=>{
      const wid=el.dataset.world;
      if(wid===cur)return;
      if(unlocked.indexOf(wid)<0){window.SFX&&SFX.deny();toastModal('尚未解锁','请沿世界通道推进解锁 '+wid);return;}
      closePanel();
      travelTo({to:wid,requireLevel:0,label:wid});
    };
  });
  // v1.9.34 副本试炼区块（并入地图面板，异步拉取剩余次数）
  const dgZone=$('dgZone');
  const dungeons=(CFG?.economy?.dungeons)||DUNGEONS;
  const lv=state&&state.core?state.core.level:1;
  // v1.9.76 组队副本不限次数，不必请求剩余次数（服务端对 perDay=0 会拒绝）
  Promise.all(dungeons.map(d=>(d.recPower||0)>0?Promise.resolve({remain:-1}):api('/api/player/action',{userId:uid(),action:'dungeon_remain',dungeonId:d.id}).catch(()=>({remain:d.perDay})))).then(rems=>{
    if(!dgZone||!dgZone.isConnected)return; // 面板已切换
    dungeons.forEach((d,i)=>d._remain=(rems[i]?.remain!=null?rems[i].remain:d.perDay));
    const diffOf={grass:1,mist:2,flame:3,frost:4};
    dgZone.innerHTML=`
      <div class="dungeon-list">${dungeons.map(d=>{
        const isParty=(d.recPower||0)>0;
        const locked=lv<d.lv, exhausted=!isParty&&((d._remain!=null?d._remain:d.perDay)<=0);
        const dis=locked||exhausted;
        const diff=diffOf[d.id]||1;
        const stars=Array.from({length:4},(_,i)=>i<diff?ICON.star(11,'#f0d590'):ICON.star(11,'#37414f')).join('');
        const tint=['#5b8f4a','#4a6b8f','#a2593c','#6f86a8'][diff-1]||'#5b8f4a';
        return `<div class="dungeon-item" style="--dc:${tint}">
          <div class="di-art">${ICON.tent(132)}</div>
          <div class="di-info">
            <div class="di-name">${d.name}<em class="di-star">${stars}</em></div>
            <div class="di-meta">推荐 Lv.${d.lv} · ${isParty?'不限次数':`今日 ${d._remain!=null?d._remain:d.perDay}/${d.perDay} 次`} · ${d.drop}</div>
          </div>
          <button class="di-enter${dis?' off':''}" data-dg="${d.id}" data-party="${isParty?1:0}" ${dis?'disabled':''}>${locked?'等级不足':(isParty?'组队挑战':exhausted?'次数用完':'进入')}</button>
        </div>`;
      }).join('')}</div>
      <div class="dg-summary">
        ${(()=>{const daily=dungeons.filter(d=>(d.recPower||0)<=0);
               const used=daily.reduce((a,d)=>a+(d.perDay-(d._remain!=null?d._remain:d.perDay)),0);
               const total=daily.reduce((a,d)=>a+d.perDay,0);
               const open=dungeons.filter(d=>lv>=d.lv).length;
               const unlimited=dungeons.filter(d=>(d.recPower||0)>0&&lv>=d.lv).length;
               return `<div class="dgs"><em>今日已挑战</em><b>${used} / ${total}</b></div>
                       <div class="dgs"><em>已解锁</em><b>${open} / ${dungeons.length}</b></div>
                       <div class="dgs"><em>主要产出</em><b>强化材料 · 装备 · 强化石</b></div>`+
                      (unlimited?`<div class="dgs"><em>不限次数</em><b>${unlimited} 个组队副本</b></div>`:'');})()}
      </div>
      <div class="dg-reset">${ICON.hourglass(13,'#f0d590')}<span>限次副本距离重置 <b>${(()=>{
        const now=new Date(); const t=new Date(now); t.setHours(24,0,0,0);
        const m=Math.floor((t-now)/60000); return Math.floor(m/60)+' 小时 '+(m%60)+' 分';
      })()}</b></span></div>`;
    dgZone.querySelectorAll('.di-enter').forEach(btn=>{
      if(btn.disabled)return;
      btn.onclick=async()=>{
        const d=dungeons.find(x=>x.id===btn.dataset.dg);
        if(!d)return;
        if(btn.dataset.party==='1'){ hideModal(); closePanel(); openPartyDungeon(d.id); return; }
        if(btn.dataset.party==='1'){ hideModal(); closePanel(); openPartyDungeon(d.id); return; }
        try{
          // 服务端权威校验等级+扣次数
          const r=await api('/api/player/action',{userId:uid(),action:'dungeon_enter',dungeonId:d.id});
          if(r.state){state=r.state;syncHud();}
          hideModal();
          closePanel();
          window.SFX&&SFX.dungeon();
          const mon={x:0,y:0,type:r.monsterId||d.mon,monId:r.monsterId||d.mon,name:d.name,elite:r.elite!=null?r.elite:d.elite,state:'idle',nodeId:'dungeon_'+d.id};
          doFight(mon);
        }catch(e){ window.SFX&&SFX.fail(); toastModal('无法进入',e.message); }
      };
    });
  }).catch(()=>{ if(dgZone&&dgZone.isConnected)dgZone.innerHTML='<div class="sec-sub">副本次数加载失败，请稍后重试</div>'; });
}
function renderIdle(){
  const body=$('panelBody');
  const inHome=homeSub==='idle';
  const backHome=inHome?'<button class="sub-back" id="backToHome">‹ 返回家园</button>':'';
  body.innerHTML=`
    ${backHome}
    <div class="sec-title">挂机设置</div>
    <div class="sec-sub">选择挂机地图 / 查看产出效率 / 领取离线收益</div>
    <div class="preview-box"><div class="pt">挂机地图</div><div class="sec-sub">翡翠草原 · 推荐 Lv.40-50 · 效率 ★★★☆</div></div>
    <div class="preview-box"><div class="pt">产出效率（每小时）</div><div class="sec-sub">经验 ~1,200 ｜ 金币 ~640 ｜ 强化材料 ~5 ｜ 回血药水 ~2</div></div>
    <div class="preview-box"><div class="pt">封顶时长</div><div class="sec-sub">8 小时（超出部分不再累计）</div></div>
    <button class="big-btn goldbtn" id="settleIdle">领取离线收益</button>`;
  if(inHome)$('backToHome').onclick=()=>{homeSub=null;window.SFX&&SFX.tab();renderHome();};
  $('settleIdle').onclick=async()=>{
    try{
      const r=await api('/api/idle/finalize',{userId:uid()});
      if(r.state){state=r.state;await applyStats(r.stats);}
      closePanel(); window.SFX&&SFX.settle(); showIdleReward(r.result);
    }catch(e){window.SFX&&SFX.fail();toastModal('结算失败',e.message);}
  };
}

/* ============================================================
   弹窗（遇怪 / 结算 / 收益 / 提示）
   ============================================================ */
function toastModal(title,sub){
  showModal(`<h2>${title}</h2><div class="msub">${sub||''}</div><button class="big-btn goldbtn" id="toastOk">确定</button>`);
  $('toastOk').onclick=()=>{ hideModal(); if(view==='world'&&panel) renderPanel(); };
}
// 点击地图上的怪物 → 交战确认（单出口：交战进战斗 / 绕行回地图）
// v1.9.36 群怪模版：弹窗按点位组合渲染敌方阵容贴图（与服务端结算同源，所见即所战）
/* ---- v1.9.42 玩家档案 / 在线玩家列表 ---- */
async function askPlayerProfile(p){
  if(view!=='world')return;
  const hadPanel=!!panel;   // v1.9.61 从公会面板点进来时，关闭后回到面板而不是恢复地图
  worldMap.active=false;
  showModal(`<h2>玩家档案</h2><div class="msub">正在查询 <b style="color:#f0d590">${p.name||'玩家'}</b> …</div>`);
  try{
    const uid2=p.userId||p.uid;
    if(!uid2){ hideModal(); toastModal('玩家档案','该玩家信息暂不可见（未登录游客）'); if(!hadPanel)worldMap.active=true; return; }
    const d=await api('/api/player/profile?userId='+encodeURIComponent(uid2));
    const cls=(CFG&&CFG.classes.classes.find(x=>x.id===d.classId)||{}).name||d.classId;
    const wn={W1:'青草平原',W2:'迷雾森林',W3:'赤焰峡谷',W4:'冰霜高原',W5:'流沙遗迹',W6:'星陨之巅',HOME:'家园'}[d.worldId]||d.worldId||'—';
    // v1.9.61 装备表补战力列
    const eqRows=(d.equips||[]).map(e=>{
      const q=Math.max(0,Math.min(7,e.quality||0));
      return `<tr><td style="color:${QCOLOR[q]};font-weight:700">${QUALITY[q]}</td><td>${SLOT_CN[e.slot]||e.slot}</td><td>${e.enhance?('+'+e.enhance):'—'}</td><td style="color:#7fe0a0">${fmt(e.power||0)}</td></tr>`;
    }).join('')||'<tr><td colspan="4">未穿戴装备</td></tr>';
    const gr={president:'会长',officer:'副会长',member:'成员'}[d.guildRole]||d.guildRole;
    showModal(`<h2>玩家档案</h2>
      <div class="guild-hero" style="margin:10px 0">
        <div class="gh-emblem">${ICON.card?ICON.card(40):ICON.shield(40,'#5a6b80')}</div>
        <div class="gh-meta">
          <div class="gh-name">${d.name} ${d.online?'<em style="color:#7fe0a0;font-style:normal">● 在线</em>':'<em style="color:#8a93a6;font-style:normal">● 离线</em>'}</div>
          <div class="gh-sub">${cls} · Lv.${d.level} · 战力 ${fmt(d.power)}</div>
          <div class="gh-sub">${d.guild?('公会 '+d.guild+(gr?('（'+gr+'）'):'')):'未加入公会'} · 所在地图 ${wn}</div>
        </div>
      </div>
      <div class="pf-attr">
        <div><span>生命</span><b>${Math.round(d.hp||0)}</b></div>
        <div><span>攻击</span><b>${Number(d.atk||0).toFixed(1)}</b></div>
        <div><span>防御</span><b>${Number(d.def||0).toFixed(1)}</b></div>
        <div><span>暴击</span><b>${(Number(d.crit||0)*100).toFixed(0)}%</b></div>
      </div>
      <div class="sec-title">穿戴装备（${(d.equips||[]).length}/4）</div>
      <table style="width:100%;border-collapse:collapse;font-size:13px">
        <thead><tr style="color:#8a93a6"><th style="text-align:left;padding:4px">品质</th><th style="text-align:left;padding:4px">部位</th><th style="text-align:left;padding:4px">强化</th><th style="text-align:left;padding:4px">战力</th></tr></thead>
        <tbody>${eqRows}</tbody>
      </table>
      <div class="enc-btns"><button class="btn-gray" id="pfClose">关闭</button></div>`);
    $('pfClose').onclick=()=>{ hideModal(); if(!hadPanel)worldMap.active=true; };
  }catch(e){
    hideModal(); if(!hadPanel)worldMap.active=true;
    toastModal('玩家档案','查询失败：'+e.message);
  }
}
async function showOnlinePlayers(){
  worldMap.active=false;
  showModal(`<h2>在线玩家</h2><div class="msub">正在获取当前在线名单…</div>`);
  try{
    const r=await api('/api/players/online');
    // v1.9.63 在线列表排除自己与游客（原来把自己也列进去，人数多算 1，还出现无法查看档案的空条目）
    const me=uid();
    const ps=(r.players||[]).filter(p=>p&&p.userId&&p.userId!==me);
    const rows=ps.map((p,i)=>{
      const cls=(CFG&&CFG.classes.classes.find(x=>x.id===p.classId)||{}).name||p.classId;
      const wn={W1:'青草平原',W2:'迷雾森林',W3:'赤焰峡谷',W4:'冰霜高原',W5:'流沙遗迹',W6:'星陨之巅',HOME:'家园'}[p.worldId]||p.worldId;
      return `<div class="gr-row">
        <div class="gr-emb">${ICON.card?ICON.card(22,'#f0d590'):ICON.shield(22,'#f0d590')}</div>
        <div class="gr-main"><div class="gr-name">${p.name} <em>${cls}</em></div>
          <div class="gr-sub">${wn} · ${(p.channel||0)+1} 线</div></div>
        <button class="btn-gray gr-view" data-uid="${p.userId||''}" data-name="${p.name}" ${p.userId?'':'disabled style="opacity:.45"'}>档案</button>
      </div>`;
    }).join('')||'<div class="sec-sub">当前没有其他玩家在线</div>';
    showModal(`<h2>在线玩家（${ps.length}）</h2><div style="max-height:340px;overflow:auto">${rows}</div>
      <div class="enc-btns"><button class="btn-gray" id="olClose">关闭</button></div>`);
    $('olClose').onclick=()=>{ hideModal(); worldMap.active=true; };
    document.querySelectorAll('.gr-view').forEach(b=>b.onclick=async()=>{
      hideModal();
      await askPlayerProfile({userId:b.dataset.uid,name:b.dataset.name});
    });
  }catch(e){
    hideModal(); worldMap.active=true;
    toastModal('在线玩家','获取失败：'+e.message);
  }
}

function askEncounter(mon){
  if(view!=='world'||panel||battlePlaying)return;
  worldMap.active=false;
  window.SFX&&SFX.alert();
  currentMonster=mon;
  const lv=monsterLevel(mon), myLv=(state&&state.core&&state.core.level)||1;
  const d=lv-myLv;
  const risk=d>=5?['极度危险','r4','建议先练级或强化装备后再来']
            :d>=3?['很强','r3','建议先去打普通怪提升等级']
            :d>=1?['有挑战','r2','注意血量，战败不扣次数']
            :d>=-1?['势均力敌','r1','可以一试']
            :['轻松','r0','适合练级'];
  const power=heroStats&&heroStats.power?heroStats.power:((state&&state.core&&state.core.battlePower)||0);
  // 敌方阵容：点位 group 下发（无则单怪兜底）
  const group=(mon.group&&mon.group.length)?mon.group:[mon.monId||mon.type||'slime'];
  const isElite = mon.kind ? mon.kind!=='normal' : !!mon.elite;   // v1.9.46 三档：boss 也标 ★
  const pre=mon.kind==='boss'?'首领 ':(isElite?'精英 ':'');
  const gangLabel=mon.gangName||mon.name;
  const tiles=group.map((g,i)=>`<div class="enc-mon"><canvas id="encMon${i}" width="96" height="116"></canvas><b>${isElite&&i===0?'★':''}${MON_NAMES[g]||g}</b></div>`).join('');
  const cntTag=group.length>1?`<span class="enc-gang-tag">群怪 · ${group.length} 只</span>`:'';
  showModal(`<h2>遭遇 ${pre}${gangLabel}${cntTag}</h2>
    <div class="msub">怪物进入警戒范围 · 是否交战？<br>战胜后有概率掉落宝箱</div>
    <div class="enc-gang" id="encGang">${tiles}</div>
    <div class="enc-foe">
      <div class="ef-row"><span>怪物等级</span><b>Lv.${lv}</b></div>
      <div class="ef-row"><span>你的等级</span><b>Lv.${myLv}</b></div>
      <div class="ef-row"><span>我方战力</span><b>${fmt(power)}</b></div>
      <div class="ef-risk ${risk[1]}">${risk[0]}<i>${risk[2]}</i></div>
    </div>
    <div class="divider"></div>
    <div class="enc-btns"><button class="${d>=3?'btn-orange':'btn-red'}" id="encFight">${d>=3?'强行挑战':'交 战'}</button><button class="btn-gray" id="encFlee">绕 行</button></div>`);
  // 阵容贴图：每只怪独立 PartActor，弹窗期间 30fps 小循环（关闭即停）
  const encActors=group.map(g=>{
    const bundle=window.MON_BUNDLE&&window.MON_BUNDLE[g]?window.MON_BUNDLE[g]:'mon_'+g;
    const a=new window.PartActor(bundle);
    return a;
  });
  Promise.all(encActors.map(a=>a.init())).then(()=>{
    encActors.forEach((a,i)=>a.play('idle'));
    const draw=()=>{
      if(document.getElementById('modalLayer').classList.contains('hidden'))return; // 弹窗已关，停止
      let alive=false;
      encActors.forEach((a,i)=>{
        const cv=document.getElementById('encMon'+i);
        if(!cv)return;
        alive=true;
        const g=cv.getContext('2d');
        g.clearRect(0,0,cv.width,cv.height);
        a.render(g,48,108,2.0,{dir:-1});
      });
      if(alive)requestAnimationFrame(draw);
    };
    draw();
  });
  $('encFight').onclick=()=>{ hideModal(); doFight(mon); };
  $('encFlee').onclick=()=>{ hideModal(); currentMonster=null; worldMap.active=true; window.SFX&&SFX.flee(); };
}
// 宝箱开启：调服务端权威接口入账（经验/金币/钻石/材料 → 存档）
/* v1.9.27 采集草药：服务端权威 roll 药水/金币入仓，点位 2 分钟 respawn */
async function onPotionPicked(p){
  let reward=null,pickErr='';
  try{
    const r=await api('/api/potion/pick',{userId:uid(),worldId:worldMap.worldId,nodeId:p.nodeId});
    reward=r.reward;
    if(r.state){state=r.state;await applyStats(r.stats);}
  }catch(e){ pickErr=e.message||''; }
  window.SFX&&(reward?SFX.heal():SFX.deny());
  if(reward){
    if(window.Quest)Quest.progress('chest',1);
    showModal(`<h2>🌿 采集草药</h2>
      <div class="msub">已放入背包（草药 2 分钟后重新长出）</div>
      <div class="divider"></div>
      <div class="reward-row"><span class="rk">回血药水</span><span class="rv g">+${reward.potions||0}</span></div>
      <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(reward.gold||0)}</span></div>
      <button class="big-btn" id="potionOk">收下 · 继续探索</button>`);
    $('potionOk').onclick=()=>{ hideModal(); window.SFX&&SFX.coin(); };
  }else{
    // 采集失败（冷却/伪造）：恢复点位可再点，弹窗提示服务端真实原因
    p.state='ready';
    showModal(`<h2>采集失败</h2><div class="msub">${pickErr||'草药尚未长成'}</div><button class="big-btn" id="potionOk">确定</button>`);
    $('potionOk').onclick=()=>hideModal();
  }
}
async function onChestOpened(chest){
  worldMap.active=false;   // 开箱动画期间冻结操作
  let reward=null, chestErr='';
  try{
    const r=await api('/api/chest/open',{userId:uid(),worldId:worldMap.worldId,nodeId:chest.nodeId,tier:chest.tier});
    reward=r.reward;
    if(r.state){state=r.state;await applyStats(r.stats);}
  }catch(e){ reward=null; chestErr=e.message||''; }
  if(reward&&window.Quest)Quest.progress('chest',1);
  if(window.SFX){ if(reward)SFX.chest(); else SFX.deny(); }
  const realTier=(reward&&reward.tier)||chest.tier;   // v1.9.45 以服务端实发档位为准
  const label={wood:'木质宝箱',silver:'银质宝箱',gold:'黄金宝箱',diamond:'钻石宝箱',crystal:'水晶宝箱'}[realTier]||'宝箱';
  setTimeout(()=>{
    const rw=reward||{exp:0,gold:0,gem:0,items:[]};
    // v1.9.35 开箱结算重构：顶部经验/金币/钻石，下方可拖动格子区展示额外物品（材料/药水/装备）
    const topRows=`<div class="reward-row"><span class="rk">${ICON.book(14,'#8fb6e0')}<em>经验</em></span><span class="rv">+${fmt(rw.exp||0)}</span></div>
      <div class="reward-row"><span class="rk">${ICON.gold(14,'#f0c040')}<em>金币</em></span><span class="rv">+${fmt(rw.gold||0)}</span></div>
      <div class="reward-row"><span class="rk">${ICON.crystal(14,'#8fd0e8')}<em>钻石</em></span><span class="rv">+${fmt(rw.gem||0)}</span></div>`;
    const qColor=QCOLOR;   // v1.9.52：复用全局 8 档品质色（原局部 5 色数组配合 min(7) 索引会取到 undefined）
    const items=rw.items||[];
    const cells=items.map(it=>{
      if(it.kind==='decompose')return `<div class="loot-cell"><div class="loot-ico" style="background:#6a5a3a">${ICON.crystal(22,'#f0d590')}</div><div class="loot-nm">${it.name||'低战力装备分解'}</div><div class="loot-qty">x${it.qty||1}</div></div>`;
      if(it.kind==='material')return `<div class="loot-cell"><div class="loot-ico" style="background:#8892a4">${ICON.crystal(22,'#2a1c08')}</div><div class="loot-nm">${it.name||'材料'}</div><div class="loot-qty">x${it.qty}</div></div>`;
      if(it.kind==='potion')return `<div class="loot-cell"><div class="loot-ico" style="background:#2e5c3f">${ICON.crystal(22,'#5fd38a')}</div><div class="loot-nm">${it.name||'回血药水'}</div><div class="loot-qty">x${it.qty}</div></div>`;
      const qc=qColor[Math.min(7,it.quality||0)];   // v1.9.46 品质 8 档，原先截断到 5 档
      return `<div class="loot-cell loot-eq" style="border-color:${qc}"><div class="loot-ico" style="background:${qc}">${ICON.bag(22,'#2a1c08')}</div><div class="loot-nm">${it.name||(it.slot||'装备')}</div><div class="loot-qty">x${it.qty||1}</div></div>`;
    }).join('');
    const gridPart=items.length
      ?`<div class="divider"></div><div class="loot-title">额外物品 · ${items.length} 项</div><div class="loot-grid" id="lootGrid">${cells}</div><div class="loot-tip">按住拖动查看全部 · 已自动入包/入仓</div>`
      :`<div class="divider"></div><div class="sec-sub">本次未获得额外物品</div>`;
    showModal(`<h2>${label} · 开启</h2>
      <div class="msub">${reward?'已入账':'开启失败 · '+(chestErr||'宝箱暂不可开')}</div>
      <div class="divider"></div>
      ${topRows}
      ${gridPart}
      <button class="big-btn" id="chestOk">收下 · 继续探索</button>`);
    // 格子区鼠标拖动滚动（手机触摸原生滚动，桌面按住拖动）
    const g=$('lootGrid');
    if(g){
      let drag=false,sy=0,ss=0;
      g.onpointerdown=e=>{drag=true;sy=e.clientY;ss=g.scrollTop;g.setPointerCapture(e.pointerId);};
      g.onpointermove=e=>{if(drag)g.scrollTop=ss-(e.clientY-sy);};
      g.onpointerup=g.onpointercancel=()=>{drag=false;};
    }
    $('chestOk').onclick=()=>{ hideModal(); worldMap.active=true; window.SFX&&SFX.coin(); if(panel)renderPanel(); };
  },520); // 等开箱动画播完
}
/* ---------- 怪物掉落宝箱（v1.9.45 起服务端权威掷档，v1.9.46 扩为三档） ----------
   档位由服务端 /api/chest/drop 权威掷出（economy.drop.monsterChest），
   本函数只负责发起登记 + 通知表现层落箱；api/uid 在 IIFE 内，故只能在这里调。
   v1.9.46：按怪物档位（normal/elite/boss）上报，档次越高高品质箱概率越高。 */
async function dropChestFor(mon){
  if(!mon) return;
  const kind = mon.kind || (mon.type==='boss_beetle'?'boss':(mon.elite?'elite':'normal'));
  const nodeId = 'drop_'+Date.now()+'_'+Math.floor(Math.random()*10000);
  try{
    const r = await api('/api/chest/drop',{userId:uid(),worldId:worldMap.worldId,nodeId,kind});
    if(r&&r.tier) worldMap.spawnChestDrop(mon, r.tier, r.nodeId||nodeId);
  }catch(e){
    console.warn('[chest] 掉落登记失败，本次不掉箱：',(e&&e.message)||e);
  }
}
window.__dropChest=dropChestFor;   // 调试钩子：自动化测试可直接触发掉落（不影响玩法）

function showBattleResult(rep,mon){
  const win=rep.result==='win';
  if(win&&window.Quest)Quest.progress('kill',1);
  const slotCN={weapon:'武器',helmet:'头盔',armor:'护甲',trinket:'饰品'};
  // v1.9.53 装备掉落改为小格子（原每件一行，10 件以上会把结算弹窗撑出屏幕、领取按钮点不到）
  // v1.9.54 按部位出图标（不再是通用背包图标）+ 完整名称；低于在穿战力已被服务端分解，只留汇总行
  const QUALCN=['普通','精良','稀有','史诗','传说','彩虹','暗影','混沌'];
  const SLOTICON={weapon:ICON.sword,helmet:ICON.helmet,armor:ICON.armor,trinket:ICON.ring};
  const dl=rep.drops||[];
  const drops=dl.length?`<div class="drop-wrap"><div class="loot-title">装备掉落 · ${dl.length} 件</div><div class="loot-grid drop-grid">${dl.map(d=>{
    const q=Math.min(7,d.quality==null?0:d.quality), qc=QCOLOR[q], qn=QUALCN[q], sn=slotCN[d.slot]||d.slot;
    const ic=(SLOTICON[d.slot]||ICON.bag)(24,qc);
    return `<div class="loot-cell loot-eq" style="border-color:${qc}" title="${sn} · ${qn}${d.enhance?' +'+d.enhance:''}"><div class="loot-ico" style="border-color:${qc}">${ic}</div><div class="loot-nm">${sn}</div><div class="loot-q" style="color:${qc}">${qn}</div></div>`;
  }).join('')}</div></div>`:'';
  // v1.9.59 自动分解要写清"每件分解出了什么"，不只是总数：逐件列出 品质·部位 → 金币/材料
  const decompRows=(rep.decomposed||[]).map(d=>{
    const q=Math.min(7,d.quality==null?0:d.quality), qc=QCOLOR[q], qn=QUALCN[q], sn=slotCN[d.slot]||d.slot;
    return `<div class="dc-row" style="border-left-color:${qc}"><span class="dc-name">${qn} · ${sn}</span><span class="dc-yield">金币 +${fmt(d.gold||0)} · 强化材料 +${fmt(d.mat||0)}</span></div>`;
  }).join('');
  const decomp=(rep.decomposedCount>0)?`<div class="drop-decomp"><div class="dc-head">${ICON.bag(15,'#c9a4e8')}<span>自动分解 <b>${rep.decomposedCount}</b> 件 <i>（战力低于在穿装备，已折成材料）</i></span><em>合计 金币 +${fmt(rep.decomposedGold||0)} · 强化材料 +${fmt(rep.decomposedMat||0)}</em></div>${decompRows?`<div class="dc-list">${decompRows}</div>`:''}</div>`:'';
  const maxHp2=(heroStats&&heroStats.hp)||0, endHp=(rep.heroHpEnd||0);
  const hpRow=(maxHp2>0)?('<div class="reward-row" title="不自动回满 · 用回血药水或采草药恢复"><span class="rk">剩余血量</span><span class="rv" style="color:#ff9c8f">'+Math.ceil(endHp)+' / '+Math.ceil(maxHp2)+'</span></div>'):'';
  showModal(`<h2 class="${win?'':'fail'}">${win?'战斗胜利':'战斗失败'}</h2>
    <div class="msub">${win?'击败':'被击败于'} ${mon.name} · 用时 ${rep.seconds||0} 秒</div>
    <div class="divider"></div>
    ${hpRow}
    ${win?`
      <div class="reward-row"><span class="rk">${ICON.book(14,'#8fb6e0')}<em>经验</em></span><span class="rv">+${fmt(rep.exp)}</span></div>
      <div class="reward-row"><span class="rk">${ICON.gold(14,'#f0c040')}<em>金币</em></span><span class="rv">+${fmt(rep.gold)}</span></div>
      ${drops}
      ${decomp}
      <button class="big-btn goldbtn" id="resBtn">领取奖励 · 返回大世界</button>`:`
      ${(()=>{
        const last=(rep.rounds||[])[(rep.rounds||[]).length-1]||{};
        const left=last.monHp!=null&&last.monHp>0?Math.round(last.monHp/(Math.max(1,...(rep.rounds||[]).map(r=>r.monHp)))*100):0;
        const rl=monsterLevel(mon), myLv=(state&&state.core&&state.core.level)||1;
        return `<div class="fail-hint">
          <div class="fh-row"><span>怪物剩余</span><b>${left}%</b></div>
          <div class="fh-row"><span>战斗回合</span><b>${(rep.rounds||[]).length}</b></div>
          <div class="fh-row"><span>等级差</span><b>${rl-myLv>0?'-'+Math.abs(rl-myLv):'+'+Math.abs(rl-myLv)}</b></div>
        </div>
        <div class="ghost-line">差距主要来自 <em>等级</em> 与 <em>装备强化</em><br>建议：先击杀同区域普通怪练级，或强化武器后再战</div>`;
      })()}
      <button class="big-btn" id="resBtn">${(state&&state.world&&state.world.worldId==='HOME')?'回家园休整':'返回大世界'}</button>
      <button class="ghost-btn" id="resEnhance">去强化装备 →</button>`}`);
  $('resBtn').onclick=async()=>{
    // 胜利：怪物立即消失（含死亡动画）+ 向服务端登记掉箱，按返回档位落箱
    if(win && currentMonster){
      if(currentMonster.actor)currentMonster.actor.playOnce('die');
      currentMonster.state='dead';
      dropChestFor(currentMonster);
      try{api('/api/monster/kill',{worldId:worldMap.worldId,nodeId:currentMonster.nodeId});}catch(e){}
    }
    hideModal();
    currentMonster=null;
    // v1.9.31 战败回家园：服务端已把存档切到 HOME（含保底 1 血），这里切场景
    if(!win && state&&state.world&&state.world.worldId==='HOME'){
      showView('world');
      worldMap.active=false;
      try{
        await loadWorld('HOME', state.world.x, state.world.y);
        toastModal('你被击败了','已回到家园休整 · 剩余血量 '+Math.ceil(rep.heroHpEnd||1)+'（使用回血药水/采草药恢复后再出发）');
      }catch(e){ worldMap.active=true; }
      worldMap.active=true;
      if(ws&&ws.readyState===1)ws.send(JSON.stringify({type:'match_join',worldId:'HOME',name:loggedUser,classId:(state&&state.core&&state.core.classId)||''}));
    } else {
      showView('world');
    }
    window.SFX&&(win?SFX.coin():SFX.panelClose());
  };  // 结算唯一出口 → 大世界 / 家园
  const re=$('resEnhance');
  if(re)re.onclick=()=>{
    hideModal();currentMonster=null;showView('world');
    roleTab='equip';openPanel('role');
    setTimeout(()=>{const b=document.querySelector('[data-tab="equip"],.tab.on');if(b)b.scrollIntoView({block:'nearest'});},260);
  };
}
function showIdleReward(res){
  // v1.9.32 修复：时长文案此前硬编码"8 小时 24 分钟"，改为按服务端结算结果动态显示
  const mins=Math.max(0,Math.round((res&&res.minutes)||0));
  const dur=mins>=60?Math.floor(mins/60)+' 小时 '+(mins%60)+' 分钟':mins+' 分钟';
  const capTxt=(res&&res.capped)?' · 已达上限，按上限结算':'';
  const hint=(res&&(res.log||''))||'';
  const lowHint=(mins<1&&!capTxt)?'<div class="sec-sub" style="color:#9fb0c8">刚离开不久，收益还在积累中 · 多挂一会儿再领取更多</div>':'';
  showModal(`<h2>离线收益</h2>
    <div class="msub">离开 ${dur||'0 分钟'}${capTxt}</div>${lowHint}
    <div class="divider"></div>
    <div class="reward-row"><span class="rk">经验</span><span class="rv">+${fmt(res&&res.exp)}</span></div>
    <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(res&&res.gold)}</span></div>
    <div class="reward-row"><span class="rk">装备</span><span class="rv g">x${res&&res.equip||0}</span></div>
    <div class="reward-row"><span class="rk">强化材料</span><span class="rv g">x${res&&res.material||0}</span></div>
    <div class="reward-row"><span class="rk">回血药水</span><span class="rv g">x${res&&res.potions||0}</span></div>
    <div class="reward-row"><span class="rk">血量</span><span class="rv" style="color:#ff9c8f">离线休息已回满</span></div>
    <button class="big-btn" id="idleClaim">一键领取 · 返回大世界</button>`);
  $('idleClaim').onclick=()=>{ hideModal(); showView('world'); };
}

/* ============================================================
   战斗自动演出（战报驱动，玩家零输入）
   ============================================================ */
function floatDmg(text,x,y,heal){
  const d=document.createElement('div');
  d.className='dmg-float'+(heal?' heal':'');d.textContent=text;
  d.style.left=x+'px';d.style.top=y+'px';
  $('fxLayer').appendChild(d);
  setTimeout(()=>d.remove(),920);
}
function logLine(cls,text){
  const box=$('battleLog');
  const d=document.createElement('div');d.className='lg '+cls;d.textContent=text;
  box.appendChild(d);
  while(box.childElementCount>6)box.removeChild(box.firstChild);
}
// v1.9.75 技能栏：每个格子加一层 CD 薄膜（顺时针扫一圈）+ 剩余秒数
let skCdMap = {};                 // skillId -> {remain, total}（单位：回合/秒）
let skLastUse = {};               // skillId -> 最近一次施放时间（用于平滑）

function skillSlots(bd){
  const all = CFG.skills.skills;
  const colors = ['sk-fire','sk-ice','sk-heal','sk-shield','sk-basic'];
  const items = bd.map(sid => all.find(x => x.id === sid) || { name:'技能', id:sid });
  items.push({ name:'普攻', id:'basic' });
  $('skillSlots').innerHTML = items.map((it,i) =>
    `<div class="skill-slot ${colors[i%colors.length]}">${it.name.charAt(0)}<small>${it.name}</small>` +
    `<div class="sk-cd" data-sk="${it.id}"></div>` +
    `<div class="sk-cdnum" data-sknum="${it.id}"></div></div>`
  ).join('');
  skCdMap = {}; paintSkillCds();
  $('skillCap').textContent = `生效技能：已装备 ${bd.length} 技能 + 普攻兜底（自动释放）`;
}

// 某个技能刚被施放：把 CD 拉满
function setSkillCd(id, ticks){
  if(!id || id === 'basic' || !(ticks > 0)) return;
  skCdMap[id] = { remain: ticks, total: ticks };
  paintSkillCds();
}
// 每次行动推进一格 CD
function tickSkillCds(){
  let dirty = false;
  for(const k in skCdMap){
    const c = skCdMap[k];
    if(c.remain > 0){ c.remain = Math.max(0, c.remain - 1); dirty = true; }
  }
  if(dirty) paintSkillCds();
}
function paintSkillCds(){
  document.querySelectorAll('#skillSlots .sk-cd').forEach(el => {
    const c = skCdMap[el.dataset.sk];
    const frac = (c && c.total > 0) ? Math.max(0, c.remain / c.total) : 0;
    el.style.setProperty('--cd', frac.toFixed(3));
  });
  document.querySelectorAll('#skillSlots .sk-cdnum').forEach(el => {
    const c = skCdMap[el.dataset.sknum];
    const n = c ? Math.ceil(c.remain) : 0;
    el.textContent = n > 0 ? String(n) : '';
    el.classList.toggle('on', n > 0);
  });
}

async function playBattle(rep,mon,bd){
  battlePlaying=true;paused=false;
  currentMonster=mon;
  // 上一场可能停在 die 姿态（战败倒地/怪物尸体），新战斗开始必须显式复位，
  // 否则第一帧会出现"尸体站起来"或"英雄躺着出手"。
  bAnim={hero:'idle',mon:'idle',heroUntil:0,monUntil:0};
  clearBattleFx();   // v1.9.51：清掉上一场残留的冲刺位移 / 飞行法球
  if(battleHeroActor)battleHeroActor.play('idle');
  // v1.9.36 群怪：按战报 monsters 重建怪物阵容 actor（与遭遇贴图一致）
  const monInfos=(rep.monsters&&rep.monsters.length)?rep.monsters:[{sprite:(mon&&(mon.sprite||mon.type))||'slime',isBoss:mon&&mon.type==='boss_beetle'}];
  battleMonInfos=monInfos;
  battleMonSig='';          // 强制重建（新阵容 + 复位姿态）
  ensureBattleActors(monInfos);
  battleMonActors.forEach((a,i)=>{a.play('idle');setMonAnim(i,'idle',0);});
  // 组合名："史莱姆×2 · 骷髅×1"（战报阵容计数）
  const mCnt={};monInfos.forEach(m=>{const n=m.name||MON_NAMES[m.sprite]||m.sprite||'怪';mCnt[n]=(mCnt[n]||0)+1;});
  const gangLabel=Object.keys(mCnt).map(k=>k+(mCnt[k]>1?'×'+mCnt[k]:'')).join(' · ');
  skillSlots(bd);
  $('battleLog').innerHTML='';
  $('monName').textContent=gangLabel;
  const lvTag=$('monLvTag');
  if(lvTag)lvTag.textContent='Lv.'+(monsterLevel(mon))+(monInfos.length>1?' · '+monInfos.length+' 只':'');
  const cdef=CFG.classes.classes.find(x=>x.id===(state&&state.core&&state.core.classId));
  if(cdef&&cdef.icon&&$('btHeroFace'))$('btHeroFace').src=RES+'characters/cards/'+cdef.icon;
  $('autoTag').classList.remove('paused');$('autoTag').textContent='自动战斗中';$('btPause').textContent='暂停';
  showView('battle');
  window.SFX&&SFX.battleStart();
  const hMax=Math.max(50,(heroStats&&heroStats.hp)||0,(rep.heroHpStart||0),...rep.rounds.map(r=>r.heroHp));
  const mMax=Math.max(50,...rep.rounds.map(r=>r.monHp));
  bHp={hero:hMax,heroMax:hMax,mon:mMax,monMax:mMax};
  updateHp();
  logLine('sys',`战斗开始 · ${rep.rounds.length} 回合 · 自动战斗${monInfos.length>1?' · 群怪 '+monInfos.length+' 只':''}`);
  const skills=CFG.skills.skills||[];
  const slotOf=i=>MON_SLOT[Math.min(Math.max(0,i|0),MON_SLOT.length-1)];
  for(const r of rep.rounds){
    while(paused && view==='battle') await sleep(120);
    if(view!=='battle') break;  // 中途退出登录则中止
    const roundNo=Math.min(r.n,30);
    $('roundVal').textContent=`${roundNo} / 30`;
    if(r.actor==='hero'){
      // v1.9.40 持续回血跳数行：绿色浮字 + 恢复日志，不播攻击动画
      if(r.action==='hot'){
        window.SFX&&SFX.heal();
        spawnFx('+'+Math.round(r.heal||0),420,430,'#7fe0a0');
        logLine('hero',`R${r.n} ${r.skillName} → 持续恢复 +${Math.round(r.heal||0)}`);
      }else{
      const sk=skills.find(x=>x.id===r.action)||{};
      const kind=r.action==='summonHit'?'summon':(sk.kind||'physical');
      setSkillCd(sk.id, sk.cd||0);   // v1.9.75 技能栏 CD
      const ti=Math.max(0,(r.monIdx||0)|0);
      const tgt=monInfos[ti]||monInfos[0];
      const tgtName=(tgt&&(tgt.name||MON_NAMES[tgt.sprite]))||'怪物';
      // 起手音：物理=挥砍，法术/治疗/增益=吟唱（不同音色，避免全用一个 hit）
      if(kind==='physical') window.SFX&&SFX.slash();
      else if(kind==='heal') window.SFX&&SFX.cast();
      else if(kind==='shield') window.SFX&&SFX.shield();
      else window.SFX&&SFX.cast();
      const sp=slotOf(ti);
      // v1.9.51 攻击位移/投射：按职业系判定（含普攻 basic 兜底），均在 150ms 抵达时点触发受击
      // 不按 kind 判定——普攻 action='basic' 会落到 physical 默认值，法师普攻也应发法球
      if(r.damage>0&&r.action!=='summonHit'){
        if(heroIsMelee()) startHeroLunge(sp);       // 战士系（战士/拳师/骑士）：冲到怪物身边再砍
        else if(heroIsCaster()) fireMagicShot(sp,r.fx);  // 法师系（法师/祭祀/术士）：法球飞向怪物
      }
      if(r.action!=='summonHit') setBAnim('hero','attack',420);
      await sleep(150);
      if(r.damage>0){
        setMonAnim(ti,'hit',300);
        // v1.9.82 召唤物协战：紫色浮字，不播英雄出手动画
        spawnFx('-'+Math.round(r.damage),sp.x,430,r.action==='summonHit'?'#c08bff':'#ff6b6b');
        if(r.fx==='fx_dmg_crit') window.SFX&&SFX.crit(); else window.SFX&&SFX.hit();
        logLine('hero',`R${r.n} ${r.skillName} → ${tgtName} -${Math.round(r.damage)}`);
        // v1.9.82 吸取：同回合伤害 + 回血一起显示
        if(r.heal>0){
          spawnFx('+'+Math.round(r.heal),420,430,'#7fe0a0');
          window.SFX&&SFX.heal();
          logLine('hero',`R${r.n} ${r.skillName} → 吸取恢复 +${Math.round(r.heal)}`);
        }
      }else if(r.heal>0){
        spawnFx('+'+Math.round(r.heal),420,430,'#7fe0a0');
        window.SFX&&SFX.heal();
        logLine('hero',`R${r.n} ${r.skillName} → 恢复 +${Math.round(r.heal)}`);
      }else{
        if(kind==='buff'||kind==='debuff') window.SFX&&SFX.buff();
        else if(kind==='shield') window.SFX&&SFX.shield();
        logLine('hero',`R${r.n} ${r.skillName}（${kind==='shield'?'持续减伤':kind==='buff'?'增益':kind==='debuff'?'减益':kind==='summon'?'召唤物登场':'效果'}）`);
      }
      }
    }else{
      const mi=Math.max(0,(r.monIdx||0)|0);
      const sp=slotOf(mi);
      window.SFX&&SFX.enemySlash();
      setMonAnim(mi,'attack',420);await sleep(150);
      setBAnim('hero','hit',300);
      spawnFx('-'+Math.round(r.damage),420,430,'#ff6b6b');
      window.SFX&&SFX.hurt();
      // v1.9.40 格挡姿态：显示减伤比例
      logLine('mon',`R${r.n} ${r.skillName} → 英雄 -${Math.round(r.damage)}${r.dr>0?`（格挡减伤 ${Math.round(r.dr*100)}%）`:''}`);
    }
    bHp.hero=r.heroHp;bHp.mon=r.monHp;updateHp();
    tickSkillCds();                 // v1.9.75 每步推进 CD
    await sleep(270);
  }
  battlePlaying=false;
  if(view!=='battle') return;  // 已退出登录
  const win=rep.result==='win';
  if(win){
    // v1.9.36 群怪：全体怪物依次倒下（错峰 120ms）
    battleMonActors.forEach((a,i)=>setMonAnim(i,'die',1600+i*120));
    window.SFX&&SFX.victory();
    spawnFx('+经验',858,468,'#f0d590');spawnFx('+金币',858,506,'#ffd76b');
  }else{
    // 600ms 只用于"HUD 提示条"的时间窗；角色动作本身是非循环 die，会一直保持倒地，
    // 直到下一次 playBattle 或离开战斗时复位（见 drawBattle 的 die 保护）。
    setBAnim('hero','die',600);
    window.SFX&&SFX.death();
  }
  const toast=document.createElement('div');
  toast.className='toast-win'+(win?'':' lose');toast.textContent=win?'战 斗 胜 利':'战 斗 失 败';
  $('fxLayer').appendChild(toast);setTimeout(()=>toast.remove(),1200);
  await sleep(1500);
  if(view!=='battle') return;
  showBattleResult(rep,mon);
}
function updateHp(){
  $('monHpFill').style.width=Math.max(0,bHp.mon/bHp.monMax*100)+'%';
  $('heroHpFill').style.width=Math.max(0,bHp.hero/bHp.heroMax*100)+'%';
  $('monHpNum').textContent=`${Math.ceil(bHp.mon)} / ${Math.ceil(bHp.monMax)}`;
  $('heroHpNum').textContent=`${Math.ceil(bHp.hero)} / ${Math.ceil(bHp.heroMax)}`;
}

/* ============================================================
   事件绑定（严格按通道）
   ============================================================ */
// 登录页
$('loginBtn').onclick=doLogin;
$('registerBtn').onclick=doRegister;
$('deviceLoginBtn').onclick=doDeviceLogin;
$('enterBtn').onclick=enterGame;
$('switchBtn').onclick=switchAccount;
$('authPwd').addEventListener('keydown',e=>{if(e.key==='Enter')doLogin();});
// 退出登录（唯一出口，全局）
$('logoutBtn').onclick=doLogout;
// 音效开关
const sfxBtn=$('sfxBtn');
function syncSfxBtn(){
  if(!sfxBtn)return;
  const m=window.SFX&&window.SFX.muted;
  sfxBtn.textContent=m?'静音':'音效';
  sfxBtn.classList.toggle('off',!!m);
}
if(sfxBtn){
  syncSfxBtn();
  sfxBtn.onclick=()=>{ if(window.SFX){ SFX.setMuted(!SFX.muted); syncSfxBtn(); if(!SFX.muted)SFX.click(); } };
}
// 底部 5 入口导航（v1.6，唯一面板入口）—— v1.9.31 家园按钮改为进入家园地图
document.querySelectorAll('.nav-badge').forEach(b=>b.onclick=()=>{ if(b.dataset.panel==='home')enterHome(); else openPanel(b.dataset.panel); });
// 面板唯一出口
$('panelBack').onclick=closePanel;
$('panelShade').onclick=closePanel;
/* 怪物等级：世界等级 + 档位加成。原实现精英 +4 / BOSS +8，导致 1 级世界里的精英
   直接是 Lv.5 —— 玩家 85 血、怪 213 血，4 秒必败且事前毫无预警。改 +2 / +5。 */
function monsterLevel(mon){
  const wl=(CFG.worlds.worlds.find(w=>w.id===worldMap.worldId)||{}).level||1;
  // v1.9.46 三档怪物：精英/首领 +2 级；首领自身 base/growth 更高，属性自然高于精英
  const kind=mon.kind||(mon.type==='boss_beetle'?'boss':(mon.elite?'elite':'normal'));
  const add=kind==='normal'?0:2;
  return Math.max(1,Math.min(60,wl+add));
}
// 交战：服务端权威结算 → 战斗演出（由 askEncounter 的"交战"按钮触发）
async function doFight(mon){
  if(view!=='world'||panel||battlePlaying)return;
  try{
    const bd=(state&&state.skills&&state.skills.slots)||['w_slash','w_guard'];
    const monLv=monsterLevel(mon);
    // v1.9.36 群怪：大世界点位战斗携带 worldId+nodeId，服务端按点位组合权威结算（与遭遇贴图同源）
    const bundle=await api('/api/battle/fight',{userId:uid(),bd,monsterId:mon.monId||mon.type,monsterLv:monLv,elite:(mon.kind==='boss'?'boss':((mon.kind==='elite'||mon.elite)?'elite':'')),
      worldId:worldMap.worldId,nodeId:mon.nodeId||''});
    if(bundle.state){state=bundle.state;await applyStats(bundle.stats);}
    currentReport=bundle.report;
    // v1.9.58 一击必杀：服务端判定怪物总血量低于普攻保底伤害时直接判胜，
    // 前端跳过整段战斗演出与血条播放，直接弹结算（不切战斗视图）
    if(bundle.report.oneShot){
      battlePlaying=false; paused=false;
      window.SFX&&SFX.coin();
      showBattleResult(bundle.report,mon);
      return;
    }
    await playBattle(bundle.report,mon,bd);
  }catch(e){toastModal('交战失败',e.message);worldMap.active=true;}
}
// 战斗控制
$('btPause').onclick=()=>{paused=!paused;$('btPause').textContent=paused?'继续':'暂停';$('autoTag').classList.toggle('paused',paused);$('autoTag').textContent=paused?'已暂停':'自动战斗中';window.SFX&&(paused?SFX.pause():SFX.resume());};
// B05：战斗/面板期间页面失焦自动暂停战斗时钟，防止后台演出
document.addEventListener('visibilitychange',()=>{
  if(document.hidden && battlePlaying && !paused){
    paused=true;$('btPause').textContent='继续';$('autoTag').classList.add('paused');$('autoTag').textContent='已暂停（切出页面）';
  }
});

/* ============================================================
   初始化
   ============================================================ */
async function init(){
  fitStage();
  try{
    await preload();
    CFG=await api('/api/config');
    if(window.Look&&CFG&&CFG.creator)Look.setManifest(CFG.creator);
    const restored=restoreSession();
    refreshLoginView();
    showView('login');              // 启动永远停在登录页（唯一入口）
    requestAnimationFrame(renderLoop);
    $('loading').classList.add('hidden');
  }catch(e){
    $('loadTxt').textContent='加载失败：'+e.message;
  }
}
init();

/* ================= v1.9.68 组队副本（大厅 / 队伍 / 多人战斗演出） ================= */
let partyPoll=null, partyLastRun=0;

function pdPowColor(my,rec){ const r=rec>0?my/rec:1; return r>=1?'#7fe0a0':r>=0.7?'#ffd76b':'#ff8a8a'; }

async function reloadPlayerState(){
  try{ const r=await api('/api/state/load',{userId:uid()}); if(r.exists){ state=r.state; await applyStats(r.stats); } }catch(e){}
}

async function openPartyDungeon(dgId){
  let info;
  try{ info=await api('/api/dungeon/party/info',{userId:uid()}); }catch(e){ toastModal('副本',e.message); return; }
  const list=info.dungeons||[];
  const d=list.find(x=>x.id===dgId)||list[0];
  if(!d){ toastModal('副本','未配置组队副本'); return; }
  const my=info.myPower||0, rec=d.recPower||0, col=pdPowColor(my,rec), ok=my>=rec;
  showModal(`<h2>${d.name}</h2>
    <div class="msub">Lv.${d.lv}+ · 最多 ${d.partyMax} 人 · ${d.unlimited?'每日不限次数':'每日限次'}</div>
    <div class="pd-pow">
      <div><span>推荐战力</span><b>${fmt(rec)}</b></div>
      <div><span>我的战力</span><b style="color:${col}">${fmt(my)}</b></div>
      <div><span>评估</span><b style="color:${col}">${ok?'达标':(my>=rec*0.7?'接近':'差距较大')}</b></div>
    </div>
    <div class="sec-sub">${d.drop||''}${ok?'':' · 战力不足时单人很可能打不过，建议组队'}</div>
    <div class="pd-btns">
      <button class="big-btn" id="pdSolo">单人挑战</button>
      <button class="big-btn goldbtn" id="pdParty">组队大厅</button>
    </div>
    <div class="enc-btns"><button class="btn-gray" id="pdClose">关闭</button></div>`);
  $('pdClose').onclick=()=>hideModal();
  $('pdSolo').onclick=()=>soloPartyRun(d.id);
  $('pdParty').onclick=()=>openPartyLobby(d.id);
}

async function soloPartyRun(dgId){
  window.SFX&&SFX.dungeon();
  showModal(`<h2>挑战中…</h2><div class="msub">正在结算这场战斗</div>`);
  try{
    await api('/api/party/create',{userId:uid(),dgId});
    const r=await api('/api/party/start',{userId:uid()});
    try{ await api('/api/party/leave',{userId:uid()}); }catch(e){}
    hideModal();
    await reloadPlayerState();
    // v1.9.70 演出与结算解耦：战斗在服务端已完成，渲染出错不能误报成"挑战失败"
    try{ playPartyBattle(r.report); }
    catch(e2){ console.error(e2); toastModal('演出异常','战斗已结算，奖励已到账'); }
  }catch(e){
    try{ await api('/api/party/leave',{userId:uid()}); }catch(e2){}
    hideModal(); toastModal('挑战失败',e.message);
  }
}

async function openPartyLobby(dgId){
  let r;
  try{ r=await api('/api/party/list',{userId:uid()}); }catch(e){ toastModal('大厅',e.message); return; }
  if(r.my){ openPartyRoom(); return; }
  const ps=(r.parties||[]).filter(p=>!dgId||p.dgId===dgId);
  const rows=ps.map(p=>{
    const leader=(p.members||[]).find(m=>m.leader)||{};
    return `<div class="pd-row">
      <div class="pd-rl"><b>${p.dgName}</b><em>${p.size}/${p.max} 人</em></div>
      <div class="pd-rs">队长 ${leader.name||'-'} · 平均战力 ${fmt(p.avgPower)}</div>
      <button class="btn-gray pd-join" data-id="${p.id}">加入</button></div>`;
  }).join('')||'<div class="sec-sub">当前没有等待中的队伍，创建一支吧</div>';
  showModal(`<h2>组队大厅</h2><div class="msub">最多 ${ps.length?ps[0].max:3} 人 · 每日不限次数</div>
    <div class="pd-list">${rows}</div>
    <div class="pd-btns"><button class="big-btn goldbtn" id="plCreate">创建队伍</button>
    <button class="btn-gray" id="plClose">关闭</button></div>`);
  $('plClose').onclick=()=>hideModal();
  $('plCreate').onclick=async()=>{
    try{ await api('/api/party/create',{userId:uid(),dgId}); openPartyRoom(); }
    catch(e){ toastModal('创建失败',e.message); }
  };
  document.querySelectorAll('.pd-join').forEach(b=>b.onclick=async()=>{
    try{ await api('/api/party/join',{userId:uid(),partyId:b.dataset.id}); openPartyRoom(); }
    catch(e){ toastModal('加入失败',e.message); }
  });
}

async function openPartyRoom(){
  if(partyPoll){ clearInterval(partyPoll); partyPoll=null; }
  partyLastRun=0;
  const paint=async()=>{
    let r;
    try{ r=await api('/api/party/status',{userId:uid()}); }catch(e){ return; }
    if(!r.my){ if(partyPoll){clearInterval(partyPoll);partyPoll=null;} hideModal(); return; }
    // 队友开战后，其他人轮询到这里也会播同一份战报
    if(r.run&&r.runAt&&r.runAt>partyLastRun){
      partyLastRun=r.runAt;
      if(partyPoll){ clearInterval(partyPoll); partyPoll=null; }
      await reloadPlayerState();
      playPartyBattle(r.run);
      return;
    }
    const me=(r.my.members||[]).find(m=>m.userId===uid())||{};
    const isLeader=!!me.leader;
    const rows=(r.my.members||[]).map(m=>`<div class="pd-m${m.online?'':' off'}">
        <span class="pd-dot"></span><b>${m.name}${m.leader?' · 队长':''}</b>
        <em>Lv.${m.level} · 战力 ${fmt(m.power)}${m.online?'':' · 离线'}</em></div>`).join('');
    showModal(`<h2>${r.my.dgName}</h2>
      <div class="msub">${r.my.size}/${r.my.max} 人 · 平均战力 ${fmt(r.my.avgPower)} · 推荐 ${fmt(r.my.recPower)}</div>
      <div class="pd-members">${rows}</div>
      <div class="pd-btns">
        ${isLeader?'<button class="big-btn goldbtn" id="prStart">开始挑战</button>':''}
        ${isLeader?'<button class="btn-gray" id="prInvite">邀请玩家</button>':''}
        <button class="btn-gray" id="prLeave">退出队伍</button>
      </div>`);
    $('prLeave').onclick=async()=>{
      try{ await api('/api/party/leave',{userId:uid()}); }catch(e){}
      if(partyPoll){clearInterval(partyPoll);partyPoll=null;}
      hideModal();
    };
    const st=$('prStart');
    if(st) st.onclick=async()=>{
      st.disabled=true; st.textContent='结算中…';
      try{
        const rr=await api('/api/party/start',{userId:uid()});
        if(partyPoll){clearInterval(partyPoll);partyPoll=null;}
        await reloadPlayerState();
        playPartyBattle(rr.report);
      }catch(e){ st.disabled=false; st.textContent='开始挑战'; toastModal('无法开始',e.message); }
    };
    const iv=$('prInvite');
    if(iv) iv.onclick=()=>openPartyInvite();
  };
  await paint();
  partyPoll=setInterval(paint,3000);
}

async function openPartyInvite(){
  let r;
  try{ r=await api('/api/players/online'); }catch(e){ toastModal('邀请',e.message); return; }
  const me=uid();
  const ps=(r.players||[]).filter(p=>p.userId&&p.userId!==me);
  const rows=ps.map(p=>`<div class="pd-row">
      <div class="pd-rl"><b>${p.name}</b><em>${p.worldId||''}</em></div>
      <button class="btn-gray pd-inv" data-uid="${p.userId}" data-name="${p.name}">邀请</button></div>`).join('')
    ||'<div class="sec-sub">当前没有其他玩家在线</div>';
  showModal(`<h2>邀请玩家</h2><div class="msub">只能邀请在线玩家</div>
    <div class="pd-list">${rows}</div>
    <div class="enc-btns"><button class="btn-gray" id="piBack">返回队伍</button></div>`);
  $('piBack').onclick=()=>openPartyRoom();
  document.querySelectorAll('.pd-inv').forEach(b=>b.onclick=async()=>{
    try{ await api('/api/party/invite',{userId:uid(),targetId:b.dataset.uid}); toastModal('已邀请',b.dataset.name+' 会在队伍面板看到邀请提示'); }
    catch(e){ toastModal('邀请失败',e.message); }
  });
}

/* ===== v1.9.71 组队副本战斗：复用打小怪的战斗场景，三个角色各自头顶血条 ===== */
let partySlots=null;          // [{name,classId,x,hpMax,hpNow,actor}]
let partyRunCtl=null;         // 取消令牌（离开战斗时中断）

// v1.9.75 组队法师法球：从该英雄手侧飞向 BOSS 胸口
function partyMagicShot(s, fx){
  const color = fx==='fx_dmg_fire'?'#ff9b4a' : fx==='fx_dmg_holy'?'#ffe9a8'
              : fx==='fx_dmg_dark'?'#b47bff' : '#7fd9ff';
  magicShots.push({x0:(s.x||420)+28, y0:470, x1:MON_SLOT[0].x, y1:MON_SLOT[0].y-120,
                   start:performance.now(), dur:165, color});
  if(magicShots.length>8) magicShots.shift();
}

function partyClear(){
  partySlots=null;
  if(partyRunCtl){ partyRunCtl.dead=true; partyRunCtl=null; }
}

// 在 drawBattle 里被调用：把队友好几个立绘画出来 + 头顶血条
function drawPartyHeroes(t,now){
  if(!partySlots) return;
  partySlots.forEach((s,i)=>{
    if(!s.actor) return;
    // v1.9.75 近战冲刺：算出本帧绘制用的 x，基准 s.x 不变
    let hx=s.x;
    if(s.lunge){
      const e=(now-s.lunge.t0)/s.lunge.dur;
      if(e>=1) s.lunge=null;
      else{
        let q;
        if(e<0.35) q=1-Math.pow(1-e/0.35,3);
        else{ const u=(e-0.35)/0.65; q=1-u*u*(3-2*u); }
        hx=s.x+(s.lunge.tx-s.x)*q;
      }
    }
    const dead = s.hpNow<=0;
    // 影子
    cvb.fillStyle='rgba(0,0,0,'+(dead?0.12:0.3)+')';
    cvb.beginPath();cvb.ellipse(hx,566,42,12,0,0,Math.PI*2);cvb.fill();
    s.actor.render(cvb,hx,560,1.6,{dir:(s.faceR?1:-1),alpha:dead?0.45:1});
    // 头顶血条
    // v1.9.73 血条贴到头顶上方：立绘脚底在 y=560、高 96*1.6，头顶约 406，血条放 360
    const w=92,h=9,bx=hx-w/2,by=Math.round(560-96*1.6)-46;
    cvb.fillStyle='rgba(0,0,0,.62)';cvb.fillRect(bx-1,by-1,w+2,h+2);
    cvb.fillStyle='#2a3a2a';cvb.fillRect(bx,by,w,h);
    const pct=Math.max(0,Math.min(1,s.hpNow/Math.max(1,s.hpMax)));
    cvb.fillStyle=dead?'#5a5f66':(pct>0.5?'#5fd38a':pct>0.25?'#ffd76b':'#ff8a8a');
    cvb.fillRect(bx,by,w*pct,h);
    cvb.strokeStyle='rgba(255,255,255,.35)';cvb.lineWidth=1;cvb.strokeRect(bx+.5,by+.5,w-1,h-1);
    // 名字 + 数字
    cvb.font='bold 12px "Microsoft YaHei",sans-serif';cvb.textAlign='center';
    cvb.fillStyle='#e6eefc';cvb.fillText(s.name||'', hx, by-6);
    cvb.font='11px "Microsoft YaHei",sans-serif';cvb.fillStyle='#cfe0f5';
    cvb.fillText(Math.max(0,Math.round(s.hpNow))+' / '+Math.round(s.hpMax), hx, by+h+12);
    cvb.textAlign='left';
  });
}

async function playPartyBattle(rep){
  if(!rep) return;
  const heroes=rep.heroes||[];
  const rounds=rep.rounds||[];
  partyClear();
  const ctl={dead:false}; partyRunCtl=ctl;

  /* ===== ① 初始化：逐项对齐单人 playBattle ===== */
  battlePlaying=true; paused=false;
  bAnim={hero:'idle',mon:'idle',heroUntil:0,monUntil:0};
  clearBattleFx();
  if(battleHeroActor) battleHeroActor.play('idle');
  // v1.9.77 三波车轮战：初始阵容取第一波
  const waveDefs=rep.waves||[];
  const firstWave=waveDefs[0]||{monsters:[{sprite:'boss_beetle',name:rep.bossName||'幽影主宰',isBoss:true,lv:30}]};
  const toInfos=(w)=>(w&&w.monsters?w.monsters:[]).map(m=>({sprite:m.sprite||'boss_beetle',name:m.name||'怪物',isBoss:!!m.isBoss,lv:m.lv||30,elite:false}));
  battleMonInfos=toInfos(firstWave);
  battleMonSig='';
  ensureBattleActors(battleMonInfos);
  battleMonActors.forEach((a,i)=>{a.play('idle');setMonAnim(i,'idle',0);});

  const me=heroes.find(h=>h.userId===uid())||heroes[0]||{};
  const meIdx=Math.max(0,heroes.findIndex(h=>h.userId===me.userId));
  skillSlots((state&&state.skills&&state.skills.slots)||[]);
  $('battleLog').innerHTML='';
  $('monName').textContent=rep.bossName||'幽影主宰';
  const lvTag=$('monLvTag');
  if(lvTag) lvTag.textContent='组队副本 · '+heroes.length+' 人';
  const cdef=CFG.classes.classes.find(x=>x.id===(state&&state.core&&state.core.classId));
  if(cdef&&cdef.icon&&$('btHeroFace')) $('btHeroFace').src=RES+'characters/cards/'+cdef.icon;
  $('autoTag').classList.remove('paused');
  $('autoTag').textContent='自动战斗中';
  $('btPause').textContent='暂停';
  showView('battle');
  window.SFX&&SFX.battleStart();

  const hMax=Math.max(50, me.hpStart||0);
  // v1.9.77 多怪：顶部血条 = 当前波所有怪血量之和
  const waveMaxOf=(w)=>Math.max(1,(w&&w.monsters?w.monsters:[]).reduce((a,m)=>a+(m.maxHp||0),0));
  const mMax=waveMaxOf(firstWave);
  bHp={hero:hMax,heroMax:hMax,mon:mMax,monMax:mMax};
  updateHp();
  logLine('sys',`组队副本 · ${heroes.length} 人 · 自动战斗`);

  /* ===== ② 队友立绘（多人样式的差异部分）===== */
  const BUNDLES=window.CLASS_BUNDLES||{};
  const xs=heroes.length===1?[420]:heroes.length===2?[360,520]:[300,430,560];
  partySlots=heroes.map((h,i)=>({
    name:h.name, classId:h.classId, x:xs[i]||420,
    hpMax:h.hpStart||1, hpNow:h.hpStart||1, actor:null,
  }));
  const SA=window.StandeeActor;
  await Promise.all(partySlots.map(async s=>{
    if(!SA) return;
    try{ const a=new SA(BUNDLES[s.classId]||'char_warrior'); await a.init(); a.play('idle'); s.actor=a; }catch(e){}
  }));
  if(ctl.dead) return;

  /* ===== ③ 逐回合演出（节奏/音效/日志与单人一致）===== */
  const skills=(CFG&&CFG.skills&&CFG.skills.skills)||[];
  const bossX=MON_SLOT[0].x;
  const total=rounds.length||1;
  let curWave=0;                 // v1.9.77 当前波次
  let curMonName='怪物';
  for(const r of rounds){
    while(paused && view==='battle') await sleep(120);
    if(ctl.dead||view!=='battle') return;
    // v1.9.77 波次切换：换阵容、重置 BOSS 血条、播报
    if(r.wave && r.wave!==curWave){
      curWave=r.wave;
      const wd=waveDefs[curWave-1];
      battleMonInfos=toInfos(wd);
      ensureBattleActors(battleMonInfos);
      battleMonActors.forEach((a,i)=>{a.play('idle');setMonAnim(i,'idle',0);});
      bHp.monMax=waveMaxOf(wd); bHp.mon=bHp.monMax; updateHp();
      $('monName')&&($('monName').textContent='第 '+curWave+' 波 · '+((wd&&wd.monsters&&wd.monsters[0]&&wd.monsters[0].name)||'怪物'));
      $('monLvTag')&&($('monLvTag').textContent=(wd&&wd.monsters?wd.monsters.length:1)+' 只 · 第 '+curWave+'/'+(waveDefs.length||1)+' 波');
      curMonName=(wd&&wd.monsters&&wd.monsters[0]&&wd.monsters[0].name)||'怪物';
      logLine('sys','—— 第 '+curWave+' 波：'+(wd&&wd.monsters?wd.monsters.length:1)+' 只'+curMonName+' ——');
      if(curWave>1){ window.SFX&&SFX.dungeon&&SFX.dungeon(); }
      await sleep(520);
    }
    // v1.9.78 死亡表现：血量归零的瞬间播倒地，之后保持倒地位姿（服务端已不再给他出手）
    if(r.heroHp) r.heroHp.forEach((hp,i)=>{
      const ps=partySlots[i]; if(!ps) return;
      if(ps.hpNow>0 && hp<=0 && ps.actor){ try{ ps.actor.playOnce('die'); }catch(e){} ps.deadPose=true; }
      ps.hpNow=hp;
    });
    // 怪群血量：当前波各怪之和
    if(r.monHp) bHp.mon=r.monHp.reduce((a,v)=>a+v,0);
    else if(r.bossHp!=null) bHp.mon=r.bossHp;
    const si=Math.max(0,(r.heroIdx||0)|0);
    const s=partySlots[si];
    if(si===meIdx) bHp.hero=s?s.hpNow:hMax;
    $('roundVal').textContent=`${r.n} / ${total}`;
    updateHp();
    if(r.actor==='hero'){
      const sk=skills.find(x=>x.id===r.action)||{};
      const kind=r.action==='hot'?'heal':(r.action==='summonHit'?'summon':(sk.kind||'physical'));
      setSkillCd(sk.id, sk.cd||0);   // v1.9.75 技能栏 CD
      if(kind==='physical') window.SFX&&SFX.slash();
      else if(kind==='shield') window.SFX&&SFX.shield();
      else window.SFX&&SFX.cast();
      // v1.9.75 近战冲到 BOSS 身边再砍；法师从自己手侧发法球（与单人一致）
      // v1.9.82 召唤物协战不播英雄出手动画（伤害来自召唤物）
      if(s && r.damage>0 && r.action!=='summonHit'){
        if(MELEE_CLASSES[s.classId]===1){
          s.lunge={t0:performance.now(), dur:430, tx:Math.max(s.x+140, MON_SLOT[0].x-170)};
        }else{
          partyMagicShot(s, r.fx);
        }
      }
      if(s&&s.actor&&r.action!=='summonHit'){ try{ s.actor.play('attack'); }catch(e){} }
      if(si===meIdx&&r.action!=='summonHit') setBAnim('hero','attack',420);
      await sleep(150);
      if(s&&s.actor&&r.action!=='summonHit'){ try{ s.actor.play('idle'); }catch(e){} }
      if(r.damage>0){
        const mi=Math.max(0,(r.monIdx||0)|0);
        setMonAnim(mi,'hit',300);
        spawnFx('-'+Math.round(r.damage), (MON_SLOT[Math.min(mi,MON_SLOT.length-1)]||{x:bossX}).x, 430, r.action==='summonHit'?'#c08bff':'#ff6b6b');
        if(r.fx==='fx_dmg_crit') window.SFX&&SFX.crit(); else window.SFX&&SFX.hit();
        logLine('hero',`R${r.n} ${s?s.name:''} ${r.skillName||''} → ${rep.bossName||'BOSS'} -${Math.round(r.damage)}`);
        // v1.9.82 吸取：同回合伤害 + 回血一起显示
        if(r.heal>0){
          window.SFX&&SFX.heal();
          spawnFx('+'+Math.round(r.heal), s?s.x:420, 440, '#7fe0a0');
          logLine('hero',`R${r.n} ${s?s.name:''} ${r.skillName||''} → 吸取恢复 +${Math.round(r.heal)}`);
        }
      }else if(r.heal>0){
        // v1.9.79 治疗落到「被治疗的人」头顶
        const tgt=(r.targetIdx!=null&&partySlots[r.targetIdx])?partySlots[r.targetIdx]:s;
        window.SFX&&SFX.heal();
        spawnFx('+'+Math.round(r.heal), tgt?tgt.x:420, 440, '#7fe0a0');
        logLine('hero',`R${r.n} ${s?s.name:''} ${r.skillName||''} → ${tgt?tgt.name:'自己'} +${Math.round(r.heal)}`);
      }else{
        // v1.9.79 格挡/增益：浮字落在目标队友头顶
        const tgt=(r.targetIdx!=null&&partySlots[r.targetIdx])?partySlots[r.targetIdx]:s;
        if(kind==='shield'){
          window.SFX&&SFX.shield();
          spawnFx('格挡', tgt?tgt.x:420, 440, '#8fd0ff');
          logLine('hero',`R${r.n} ${s?s.name:''} ${r.skillName||''} → ${(tgt&&tgt!==s)?tgt.name:'自己'}（减伤 ${Math.round((sk.drPct||0)*100)}%，${sk.drRounds||1} 回合）`);
        }else{
          logLine('hero',`R${r.n} ${s?s.name:''} ${r.skillName||''}（${kind==='buff'?'增益':kind==='debuff'?'减益':kind==='summon'?'召唤物登场':'效果'}）`);
        }
      }
    }else{
      window.SFX&&SFX.enemySlash();
      setMonAnim(0,'attack',420);
      await sleep(150);
      if(si===meIdx) setBAnim('hero','hit',300);
      spawnFx('-'+Math.round(r.damage||0), s?s.x:420, 440, '#ff6b6b');
      window.SFX&&SFX.hurt();
      logLine('mon',`R${r.n} ${rep.bossName||'BOSS'} → ${s?s.name:'队友'} -${Math.round(r.damage||0)}`);
    }
    // 本波全灭：整排怪物倒地（最后一波即通关）
    if(r.monHp && r.monHp.length && r.monHp.every(v=>v<=0)){
      battleMonActors.forEach((a,i)=>setMonAnim(i,'die',1400+i*120));
      await sleep(500);
    }
    tickSkillCds();                 // v1.9.75 每步推进 CD
    await sleep(210);
  }
  if(ctl.dead||view!=='battle') return;

  /* ===== ④ 结算 ===== */
  battlePlaying=false;
  const win=rep.result==='win';
  if(win){ battleMonActors.forEach((a,i)=>setMonAnim(i,'die',1600+i*120)); window.SFX&&SFX.victory(); }
  else{ window.SFX&&SFX.death(); }
  const tt=document.createElement('div');
  tt.className='toast-win'+(win?'':' lose'); tt.textContent=win?'战 斗 胜 利':'战 斗 失 败';
  $('fxLayer').appendChild(tt); setTimeout(()=>tt.remove(),1200);
  await sleep(1500);
  if(view!=='battle') return;
  const wrows=(rep.waves||[]).map(w=>`<div class="pd-m"><span class="pd-dot${w.result==='win'?'':' off'}"></span>
      <b>第 ${w.index} 波</b><em>${w.monsters?w.monsters.length:1} 只 · ${w.result==='win'?'通过':'失败'} · ${w.rounds} 回合</em></div>`).join('');
  const rows=(rep.heroes||[]).map(h=>`<div class="pd-m"><span class="pd-dot${h.alive?'':' off'}"></span><b>${h.name}</b>
      <em>${h.alive?'存活':'倒下'} · 剩余 ${Math.round(h.hpEnd)} · 输出 ${fmt(Math.round(h.damage||0))}</em></div>`).join('');
  showModal(`<h2 class="${win?'':'fail'}">${win?'战斗胜利':'战斗失败'}</h2>
    <div class="msub">通过 ${rep.waveDone||0}/${rep.waveTotal||3} 波 · ${rep.partyMult} 倍人数加成</div>
    <div class="sec-title">关卡进度</div>${wrows}
    <div class="sec-title">成员结算</div>${rows}
    <div class="reward-row"><span class="rk">经验</span><span class="rv">+${fmt(rep.exp||0)}</span></div>
    <div class="reward-row"><span class="rk">金币</span><span class="rv">+${fmt(rep.gold||0)}</span></div>
    <div class="enc-btns"><button class="big-btn goldbtn" id="partyOk">收下</button></div>`);
  $('partyOk').onclick=async()=>{
    hideModal(); partyClear(); showView('world');
    await reloadPlayerState();
  };
}

/* 邀请接收：未在队伍中时轻量轮询（8s），有邀请就弹窗 */
let inviteTimer=null;
function startInviteWatch(){
  if(inviteTimer) return;
  inviteTimer=setInterval(async()=>{
    if(!state||!state.core) return;
    if(panel||view!=='world') return;                       // 面板/战斗中不打扰
    if(!document.getElementById('modalLayer').classList.contains('hidden')) return;
    let r; try{ r=await api('/api/party/status',{userId:uid()}); }catch(e){ return; }
    if(r.my||!r.invite) return;
    const inv=r.invite;
    showModal(`<h2>组队邀请</h2>
      <div class="msub"><b style="color:#f0d590">${inv.fromName}</b> 邀请你加入队伍</div>
      <div class="pd-btns">
        <button class="big-btn goldbtn" id="invYes">接受</button>
        <button class="btn-gray" id="invNo">拒绝</button>
      </div>`);
    $('invYes').onclick=async()=>{
      try{ await api('/api/party/accept',{userId:uid()}); hideModal(); openPartyRoom(); }
      catch(e){ hideModal(); toastModal('接受失败',e.message); }
    };
    $('invNo').onclick=async()=>{
      try{ await api('/api/party/decline',{userId:uid()}); }catch(e){}
      hideModal();
    };
  },8000);
}
window.__battleReplay=playBattle;   // 调试钩子：自动化测试回放服务端战报（不影响玩法）
})();
